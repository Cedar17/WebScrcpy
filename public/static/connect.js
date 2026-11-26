// TCP/MSE live playback settings (seconds, except the watchdog interval).
const tcpLatency = sync => sync ? {target:0.18, accelerate:0.30, seek:0.65, rate:1.10} : {target:0.08, accelerate:0.15, seek:0.35, rate:1.15};

// Load the local Android session configuration from sessionStorage.
var CONFIG = (function () {

    // Try to load from sessionStorage first (set by console.js)
    const stored = sessionStorage.getItem('webscreen_device_configs_now');
    console.log("Stored config:", stored);
    if (stored) {
        try {
            const parsed = JSON.parse(stored);
            console.log("Using stored config:", parsed);
            // Clear it after reading to avoid stale data
            // sessionStorage.removeItem('webscreen_device_configs_now');
            return parsed;
        } catch (e) {
            console.warn('Failed to parse stored config:', e);
        }
    }

    // No active session: the caller returns to the console.
    return {
        device_type: "none",
        device_id: "",
        device_ip: "0",
        device_port: "0",
        av_sync: false,
        driver_config: {
            video_codec: "h264",
            audio: "true",
            audio_codec: "opus",
            video_bit_rate: "8000000"
        }
    };
})();

// Same-origin WebSocket: original control packets and timestamped media.
let tcpCurrent = null;
let tcpRetry = 0;
let tcpReconnect = null;
let tcpStopped = false;
let tcpUI = null;

function sendDataChannelMessage(channel, msg) {
    if (channel && channel.readyState === 'open') channel.send(msg);
}

function tcpFeedback(data) {
    if (!data.length) return;
    const message = new TextDecoder().decode(data.subarray(1));
    if (data[0] === 0x17) {
        if (navigator.clipboard) navigator.clipboard.writeText(message).catch(() => tcpCopyFallback(message));
        else tcpCopyFallback(message);
    } else if (data[0] === 0x64) showToast(message, 3000);
}
function tcpCopyFallback(message) {
    const field = document.createElement('textarea');
    field.value = message;
    field.style.position = 'fixed';
    field.style.opacity = '0';
    document.body.appendChild(field);
    field.select();
    let copied = false;
    try { copied = document.execCommand('copy'); } catch (_) {}
    field.remove();
    if (!copied) window.prompt('Copy from device:', message);
}
function tcpClose() {
    const s = tcpCurrent;
    tcpCurrent = null;
    if (!s) return;
    clearInterval(s.watchdog);
    s.socket.onclose = null;
    s.socket.close();
    if (s.muxer) s.muxer.destroy();
    if (s.audio) s.audio.destroy();
}
function tcpRequestKey(s) {
    if (s.socket.readyState === WebSocket.OPEN) s.socket.send(Uint8Array.of(0x63,0));
}
function tcpScheduleRetry() {
    if (tcpStopped || tcpReconnect !== null) return;
    tcpReconnect = setTimeout(() => {
        tcpReconnect = null;
        start();
    }, Math.min(8000,500 * Math.pow(2,Math.min(tcpRetry++,4))));
}
function tcpFail(s, message, retry = true) {
    if (tcpCurrent !== s) return;
    if (message) showToast(message,3000);
    tcpClose();
    if (retry) tcpScheduleRetry();
}
function tcpCodecSupported(codec) {
    if (codec === 'h265') return ['hvc1.1.6.L93.B0','hev1.1.6.L93.B0','hvc1.1.6.L153.B0'].some(c => MediaSource.isTypeSupported('video/mp4; codecs="'+c+'"'));
    return ['avc1.640033','avc1.42E01E','avc1.4D401E'].some(c => MediaSource.isTypeSupported('video/mp4; codecs="'+c+'"'));
}
async function start() {
    if (tcpStopped || tcpCurrent) return;
    if (CONFIG.device_type === 'none') {
        showToast(i18n.t('error_no_device_config'),3000);
        return;
    }
    if (!window.MediaSource || !window.JMuxer || !tcpCodecSupported('h264')) {
        showToast('This browser cannot play H.264 using Media Source Extensions.',5000);
        return;
    }
    const config = {...CONFIG,driver_config:{...CONFIG.driver_config}};
    if (config.driver_config.video_codec === 'h265' && !tcpCodecSupported('h265')) {
        config.driver_config.video_codec = 'h264';
        showToast('H.265 unavailable in this browser; using H.264.',3000);
    }
    const video = document.getElementById('remoteVideo');
    video.srcObject = null;
    const socket = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://')+location.host+'/screen/ws');
    socket.binaryType = 'arraybuffer';
    window.ws = socket;
    const s = {socket,muxer:null,audio:null,ready:false,lastPTS:null,lastArrival:null,params:[],paramBytes:0,queue:[],queueBytes:0,lastVideo:Date.now(),opened:Date.now(),watchdog:null,firstFrame:false};
    tcpCurrent = s;
    const channel = label => ({
        label,
        get readyState() { return s.ready && socket.readyState===WebSocket.OPEN ? 'open':'closed'; },
        get bufferedAmount() { return socket.bufferedAmount; },
        send(data) {
            if (this.readyState !== 'open') return;
            if (socket.bufferedAmount > 1024*1024) {
                tcpFail(s,'TCP control connection congested');
                return;
            }
            socket.send(data);
        }
    });
    window.dataChannelOrdered = channel('control-ordered');
    window.dataChannelUnordered = channel('control-unordered');
    socket.onopen = () => socket.send(JSON.stringify(config));
    socket.onerror = () => console.warn('[tcp] websocket error');
    socket.onclose = () => tcpFail(s,'TCP connection disconnected');
    function feedVideo(payload, pts, flags) {
        if (flags & 1) {
            s.paramBytes += payload.length;
            if (s.paramBytes > 1024*1024) throw Error('Oversized video configuration');
            s.params.push(payload.slice());
            return;
        }
        if (s.params.length) {
            const frame = new Uint8Array(s.paramBytes+payload.length);
            let off=0;
            for (const p of s.params) { frame.set(p,off); off+=p.length; }
            frame.set(payload,off);
            payload=frame;
            s.params=[]; s.paramBytes=0;
        }
        const arrival = performance.now();
        const stamp = config.use_local_timestamp ? arrival*1000 : pts;
        const fps = Math.max(1,Number(config.driver_config.max_fps)||60);
        let duration = s.lastPTS===null ? 1000/fps : (stamp-s.lastPTS)/1000;
        s.lastPTS=stamp;
        // A static screen can have very large PTS gaps; do not create seconds of buffered video.
        const nominal = 1000/fps;
        const idle = s.lastArrival === null ? 0 : arrival-s.lastArrival;
        s.lastArrival = arrival;
        if (duration<=0 || duration>Math.max(120,4*nominal) || idle>Math.max(120,4*nominal)) duration=nominal;
        duration = Math.min(duration,config.av_sync ? Math.max(120,nominal) : Math.max(40,2*nominal));
        duration=Math.max(1,Math.round(duration));
        const item={video:payload,duration,isLastVideoFrameComplete:true};
        if (!s.ready) {
            s.queueBytes+=payload.length;
            if(s.queueBytes>16*1024*1024 || s.queue.length>=120) throw Error('Video player initialization timed out');
            s.queue.push(item);
        } else {
            s.muxer.feed(item);
        }
        s.lastVideo=Date.now();
        s.firstFrame=true;
        tcpRetry=0;
        if(video.paused) video.play().catch(()=>{});
    }
    socket.onmessage = event => {
        if (tcpCurrent!==s) return;
        try {
            if (typeof event.data==='string') {
                const msg=JSON.parse(event.data);
                if (msg.status==='error') { tcpFail(s,msg.message,false); return; }
                if (msg.stage!=='tcp_metainfo' || s.muxer) return;
                const caps=msg.capabilities||{};
                const meta=msg.media_meta||{};
                const codec=(meta.video_codec||config.driver_config.video_codec||'h264').toLowerCase();
                if (!tcpCodecSupported(codec)) { tcpFail(s,'Unsupported stream codec: '+codec,false); return; }
                s.muxer=new JMuxer({
                    node:video,mode:'video',videoCodec:codec==='h265'?'H265':'H264',
                    fps:Number(meta.fps)||Number(config.driver_config.max_fps)||60,
                    flushingTime:0,maxDelay:60000,clearBuffer:true,
                    onReady:() => {
                        if (tcpCurrent!==s) return;
                        s.ready=true;
                        for(const item of s.queue) s.muxer.feed(item);
                        s.queue=[]; s.queueBytes=0;
                        video.play().catch(()=>{});
                    },
                    onUnsupportedCodec: codec => tcpFail(s,'Unsupported video codec: '+codec,false),
                    onError: e => {console.warn('[tcp video]',e);tcpRequestKey(s);}
                });
                if(caps.can_audio) {
                    if(window.TCPAudio && TCPAudio.supported() && String(meta.audio_codec||'opus').trim()==='opus') {
                        s.audio=new TCPAudio(config.av_sync,video);
                    } else showToast('Audio codec is not supported by this browser.',3000);
                }
                if(!tcpUI) tcpUI=updateUIBasedOnCapabilities(caps).catch(e=>{tcpUI=null;console.error(e);});
                return;
            }
            const data=new Uint8Array(event.data);
            if(data.length<10) return;
            const pts=Number(new DataView(data.buffer,data.byteOffset,data.byteLength).getBigUint64(2,false));
            const payload=data.subarray(10);
            if(data[0]===1 && s.muxer) feedVideo(payload,pts,data[1]);
            else if(data[0]===2 && s.audio) s.audio.feed(payload,pts,!!(data[1]&1));
            else if(data[0]===3) tcpFeedback(payload);
        } catch(e) { console.error('[tcp]',e); tcpFail(s,e.message); }
    };
    s.watchdog=setInterval(()=>{
        if(tcpCurrent!==s) return;
        if(!s.firstFrame && Date.now()-s.opened>30000) {tcpFail(s,'No video frames received');return;}
        if(s.ready && video.buffered.length) {
            const end=video.buffered.end(video.buffered.length-1);
            const lag=end-video.currentTime;
            const p = tcpLatency(config.av_sync);
            if (!video.seeking && lag>p.seek) {
                video.playbackRate=1;
                video.currentTime=Math.max(video.buffered.start(video.buffered.length-1),end-p.target);
            } else if (!video.seeking) {
                const rate=lag>p.accelerate?p.rate:1;
                if(video.playbackRate!==rate) video.playbackRate=rate;
            }
        }
    },100);
}
window.addEventListener('pagehide',()=>{
    tcpStopped=true;
    if(tcpReconnect!==null) clearTimeout(tcpReconnect);
    tcpReconnect=null;
    tcpClose();
});
window.addEventListener('pageshow',event=>{
    if(event.persisted){tcpStopped=false;start();}
});
document.addEventListener('DOMContentLoaded',()=>{
    const video=document.getElementById('remoteVideo');
    if(video) {
        const resize=()=>{if(typeof updateVideoCache==='function') updateVideoCache();if(typeof triggerCheck==='function') triggerCheck();};
        video.addEventListener('loadedmetadata',resize);
        video.addEventListener('resize',resize);
    }
});

function loadScript(src) {
    return new Promise((resolve, reject) => {
        // Check if script is already loaded
        if (document.querySelector(`script[src="${src}"]`)) {
            resolve();
            return;
        }
        const script = document.createElement('script');
        script.src = src;
        script.onload = resolve;
        script.onerror = reject;
        document.head.appendChild(script);
    });
}

async function updateUIBasedOnCapabilities(caps) {
    if (!caps) return;

    // Helper to show elements
    const show = (selector) => {
        document.querySelectorAll(selector).forEach(el => el.style.display = ''); // Remove inline display:none to revert to CSS
    };

    // Handle Control
    if (caps.can_control) {
        // Load control scripts
        try {
            if (caps.is_android) {
                await loadScript('/static/capabilities/keyboard.js');
                await loadScript('/static/capabilities/touch.js');
                await loadScript('/static/capabilities/scroll.js');
                show('.feature-android-buttons');
                show('.feature-control');
            }

            console.log("Control scripts loaded");
        } catch (e) {
            console.error("Failed to load control scripts", e);
        }

        // Handle Clipboard
        if (caps.can_clipboard) {
            try {
                await loadScript('/static/capabilities/clipboard.js');
                show('.feature-clipboard');
            } catch (e) {
                console.error("Failed to load clipboard script", e);
            }
        }
        // Handle UHID
        if (caps.can_uhid) {
            try {
                await loadScript('/static/capabilities/uhid_mouse.js');
                await loadScript('/static/capabilities/uhid_keyboard.js');
                await loadScript('/static/capabilities/uhid_gamepad.js');
                show('.feature-uhid');
                console.log("UHID scripts loaded");
            } catch (e) {
                console.error("Failed to load UHID scripts", e);
            }
        }

        await loadScript('/static/capabilities/keep_screen_on.js');

    }

}
