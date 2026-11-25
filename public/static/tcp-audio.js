/* Opus over ordinary HTTP: package the existing scrcpy packets into WebM
 * for Media Source Extensions. No WebCodecs, WebRTC or external service. */
(function () {
    'use strict';
    const mime = 'audio/webm; codecs="opus"';
    const cat = (...parts) => {
        const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
        let off = 0;
        for (const p of parts) { out.set(p, off); off += p.length; }
        return out;
    };
    const bytes = (...v) => Uint8Array.of(...v);
    const str = s => new TextEncoder().encode(s);
    const uint = n => {
        const a = [];
        do { a.unshift(n % 256); n = Math.floor(n / 256); } while (n);
        return Uint8Array.from(a);
    };
    function size(n) {
        let width = 1;
        while (n >= Math.pow(2, 7 * width) - 1) width++;
        const out = new Uint8Array(width);
        for (let i = width - 1; i >= 0; i--) { out[i] = n % 256; n = Math.floor(n / 256); }
        out[0] |= 1 << (8 - width);
        return out;
    }
    const el = (id, data) => cat(Uint8Array.from(id), size(data.length), data);
    const u = (id, n) => el(id, uint(n));
    const s = (id, text) => el(id, str(text));
    const master = (id, ...children) => el(id, cat(...children));
    function opusHead() {
        const h = new Uint8Array(19);
        h.set(str('OpusHead')); h[8] = 1; h[9] = 2;
        const v = new DataView(h.buffer);
        v.setUint16(10, 312, true); v.setUint32(12, 48000, true);
        return h;
    }
    function init(head) {
        const preSkip = new DataView(head.buffer, head.byteOffset, head.byteLength).getUint16(10, true);
        const rate = new Uint8Array(8);
        new DataView(rate.buffer).setFloat64(0, 48000);
        const ebml = master([0x1a,0x45,0xdf,0xa3],
            u([0x42,0x86],1),u([0x42,0xf7],1),u([0x42,0xf2],4),u([0x42,0xf3],8),
            s([0x42,0x82],'webm'),u([0x42,0x87],4),u([0x42,0x85],2));
        const info = master([0x15,0x49,0xa9,0x66],
            u([0x2a,0xd7,0xb1],1000000),s([0x4d,0x80],'WebScrcpy'),s([0x57,0x41],'WebScrcpy'));
        const track = master([0xae],u([0xd7],1),u([0x73,0xc5],1),u([0x83],2),
            s([0x86],'A_OPUS'),el([0x63,0xa2],head),
            u([0x56,0xaa],Math.round(preSkip*1e9/48000)),u([0x56,0xbb],80000000),
            master([0xe1],el([0xb5],rate),u([0x9f],head[9])));
        return cat(ebml,bytes(0x18,0x53,0x80,0x67,0x01,0xff,0xff,0xff,0xff,0xff,0xff,0xff),
            info,master([0x16,0x54,0xae,0x6b],track));
    }
    function cluster(ms, packet) {
        return master([0x1f,0x43,0xb6,0x75],u([0xe7],ms),
            el([0xa3],cat(bytes(0x81,0,0,0x80),packet)));
    }
    class TCPAudio {
        static supported() {
            return !!window.MediaSource && MediaSource.isTypeSupported(mime);
        }
        constructor(sync, video) {
            this.sync = sync;
            this.video = video;
            this.queue = [];
            this.queuedBytes = 0;
            this.head = opusHead();
            this.started = false;
            this.base = null;
            this.last = -1;
            this.closed = false;
            this.audio = document.createElement('audio');
            this.audio.autoplay = true;
            this.audio.style.display = 'none';
            document.body.appendChild(this.audio);
            this.media = new MediaSource();
            this.url = URL.createObjectURL(this.media);
            this.audio.src = this.url;
            this.unlock = () => { this.audio.play().catch(() => {}); };
            document.addEventListener('pointerdown',this.unlock);
            document.addEventListener('keydown',this.unlock);
            this.media.addEventListener('sourceopen',() => {
                if (this.closed) return;
                try {
                    this.sb = this.media.addSourceBuffer(mime);
                    this.sb.addEventListener('updateend',() => this.drain());
                    this.sb.addEventListener('error',() => this.destroy());
                    this.drain();
                } catch (e) { console.warn('[tcp audio]', e); this.destroy(); }
            },{once:true});
            this.timer = setInterval(() => this.catchUp(),500);
        }
        feed(packet, pts, config) {
            if (this.closed || !packet.length) return;
            // Android MediaCodec may send OpusHead as a separate codec packet.
            if (packet.length >= 19 && String.fromCharCode(...packet.subarray(0,8)) === 'OpusHead') {
                if (!this.started) this.head = packet.slice(0,19);
                return;
            }
            if (config) return;
            if (!this.started) {
                this.started = true;
                this.push(init(this.head));
                this.base = pts;
            }
            let ms = Math.max(0,Math.round((pts-this.base)/1000));
            // Some drivers omit packet timestamps; maintain a monotonic timeline.
            if (ms <= this.last) ms = this.last+20;
            this.last = ms;
            this.push(cluster(ms,packet));
        }
        push(data) {
            if (this.closed) return;
            if (this.queuedBytes+data.length > 2*1024*1024) { this.destroy(); return; }
            this.queue.push(data);
            this.queuedBytes += data.length;
            this.drain();
        }
        drain() {
            if (this.closed || !this.sb || this.sb.updating || !this.queue.length) return;
            try {
                const b = this.sb.buffered;
                if (b.length && this.audio.currentTime-b.start(0)>15) {
                    this.sb.remove(b.start(0),this.audio.currentTime-3);
                    return;
                }
                const data = cat(...this.queue.splice(0,32));
                this.queuedBytes -= data.length;
                this.sb.appendBuffer(data);
                if (this.audio.paused) this.unlock();
            } catch (e) { console.warn('[tcp audio append]',e); this.destroy(); }
        }
        catchUp() {
            if (this.closed || !this.audio.buffered.length) return;
            const end = this.audio.buffered.end(this.audio.buffered.length-1);
            const lag = end-this.audio.currentTime;
            const target = this.sync && this.video.buffered.length ?
                Math.max(0.08,this.video.buffered.end(this.video.buffered.length-1)-this.video.currentTime) : 0.15;
            if (lag > target+0.6) this.audio.currentTime = Math.max(this.audio.buffered.start(0),end-target);
            else this.audio.playbackRate = lag>target+0.12 ? 1.08 : 1;
        }
        destroy() {
            if (this.closed) return;
            this.closed = true;
            clearInterval(this.timer);
            document.removeEventListener('pointerdown',this.unlock);
            document.removeEventListener('keydown',this.unlock);
            this.audio.pause();
            this.audio.removeAttribute('src');
            this.audio.load();
            this.audio.remove();
            URL.revokeObjectURL(this.url);
            this.queue.length = 0;
        }
    }
    window.TCPAudio = TCPAudio;
})();
