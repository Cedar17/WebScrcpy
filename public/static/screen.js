const videoElementFs = document.getElementById('remoteVideo');
// 获取视频容器，全屏容器而不是视频元素本身，可以避免显示浏览器默认的视频控件
const videoContainer = document.querySelector('.video-container');

// 阻止点击视频时的默认行为（如暂停/播放）
videoElementFs.addEventListener('click', (event) => {
    event.preventDefault();
    event.stopPropagation();
});

// 阻止双击默认行为
videoElementFs.addEventListener('dblclick', (event) => {
    event.preventDefault();
    event.stopPropagation();
});

function fullscreenPlayer() {
    // 对容器进行全屏
    const target = videoContainer || videoElementFs;

    if (!document.fullscreenElement && !document.webkitFullscreenElement) {
        const requestMethod = target.requestFullscreen || 
                              target.webkitRequestFullscreen || 
                              target.mozRequestFullScreen || 
                              target.msRequestFullscreen;

        if (requestMethod) {
            requestMethod.call(target).catch(err => {
                console.error(`Error attempting to enable full-screen mode: ${err.message} (${err.name})`);
            });
        }
    } else {
        const exitMethod = document.exitFullscreen || 
                           document.webkitExitFullscreen || 
                           document.mozCancelFullScreen || 
                           document.msExitFullscreen;

        if (exitMethod) {
            exitMethod.call(document);
        }
    }
}

function handleFullscreenChange() {
    const isFullscreen = !!document.fullscreenElement || 
                         !!document.webkitFullscreenElement || 
                         !!document.mozFullScreenElement || 
                         !!document.msFullscreenElement;

    if (isFullscreen) {
        // 样式调整以适应全屏
        // 注意：如果是容器全屏，video 元素的尺寸通常由 CSS (max-width/height: 100%) 自动控制
        // 但我们强制设置一下以防万一
        videoElementFs.style.width = '100%';
        videoElementFs.style.height = '100%';
        videoElementFs.style.objectFit = 'contain';
        
        // 关键：拦截浏览器默认行为
        // touch-action: none 阻止浏览器处理触摸手势（如缩放、滚动）
        videoElementFs.style.touchAction = 'none';
        // overscroll-behavior: none 阻止滚动链和橡皮筋效果（如页面顶部的下拉刷新或边缘导航）
        videoElementFs.style.overscrollBehavior = 'none';
        
    } else {
        // 退出全屏时恢复
        videoElementFs.style.width = '';
        videoElementFs.style.height = '';
        videoElementFs.style.objectFit = '';
        
        videoElementFs.style.touchAction = '';
        videoElementFs.style.overscrollBehavior = '';
    }
    
    // 触发一次 resize 事件以更新触摸坐标计算缓存
    window.dispatchEvent(new Event('resize'));
}

document.addEventListener('fullscreenchange', handleFullscreenChange);
document.addEventListener('webkitfullscreenchange', handleFullscreenChange);
document.addEventListener('mozfullscreenchange', handleFullscreenChange);
document.addEventListener('MSFullscreenChange', handleFullscreenChange);

const remoteVideo = document.getElementById('remoteVideo');

window.isUHIDMouseEnabled = false;
// 缓存视频元素的位置和尺寸，避免频繁调用 getBoundingClientRect
window.cachedRect = {
    VideoRect: null,
    ContentRect: { left: 0, top: 0, width: 0, height: 0 }
}
// 更新缓存的视频尺寸和位置
function updateVideoCache() {
    if (remoteVideo.videoWidth && remoteVideo.videoHeight) {
        window.cachedRect.VideoRect = remoteVideo.getBoundingClientRect();
        
        const elWidth = window.cachedRect.VideoRect.width;
        const elHeight = window.cachedRect.VideoRect.height;
        const vidWidth = remoteVideo.videoWidth;
        const vidHeight = remoteVideo.videoHeight;
        
        if (elWidth === 0 || elHeight === 0) return false;

        const vidRatio = vidWidth / vidHeight;
        const elRatio = elWidth / elHeight;

        let drawWidth, drawHeight, startX, startY;

        if (elRatio > vidRatio) {
            // 元素比视频宽 (Pillarbox: 左右黑边)
            drawHeight = elHeight;
            drawWidth = drawHeight * vidRatio;
            startY = 0;
            startX = (elWidth - drawWidth) / 2;
        } else {
            // 元素比视频高 (Letterbox: 上下黑边)
            drawWidth = elWidth;
            drawHeight = drawWidth / vidRatio;
            startX = 0;
            startY = (elHeight - drawHeight) / 2;
        }
        
        window.cachedRect.ContentRect = {
            left: startX,
            top: startY,
            width: drawWidth,
            height: drawHeight
        };
        return true;
    }
    return false;
}

/**
 * 自动监听视频元素尺寸变化
 * 代替 window.resize，能捕捉 CSS 旋转、缩放等引起的尺寸改变
 */
function initVideoObserver() {
    const remoteVideo = document.getElementById('remoteVideo');
    if (!remoteVideo) return;

    const observer = new ResizeObserver(entries => {
        for (let entry of entries) {
            // 使用防抖，防止短时间内频繁触发
            if (window.updateCacheTimer) clearTimeout(window.updateCacheTimer);
            
            window.updateCacheTimer = setTimeout(() => {
                console.log("Detected video size change, updating cache...");
                if (typeof updateVideoCache === 'function') {
                    updateVideoCache();
                }
            }, 100);
        }
    });

    // 开始观察视频元素
    observer.observe(remoteVideo);
}
initVideoObserver();

// 指针锁定 API (Pointer Lock) - 用于更好的鼠标控制
function requestPointerLock() {

    const requestMethod = remoteVideo.requestPointerLock ||
        remoteVideo.mozRequestPointerLock ||
        remoteVideo.webkitRequestPointerLock;

    if (requestMethod) {
        requestMethod.call(remoteVideo);
    }
}

function exitPointerLock() {
    const exitMethod = document.exitPointerLock ||
        document.mozExitPointerLock ||
        document.webkitExitPointerLock;

    if (exitMethod) {
        exitMethod.call(document);
    }
}
