// The Jellyfin player page (templates/views/jellyfin/play.html): movi-player
// on /jellyfin/:id/stream with the subtitle picker, plus the resume point and
// the up-next prompt (lib/jellyfin/play.js).
import av from '../lib/av';

let undo = null;

av(async function () {
    const root = this.closest('#jellyfin-player') || this;
    const { initPlayer } = await import('../lib/player/Player');
    await initPlayer(root);
    const video = root.querySelector('.player');
    if (video) {
        const { wirePlayPage } = await import('../lib/jellyfin/play');
        undo = wirePlayPage(root, video);
    }
}, async function () {
    if (undo) undo();
    undo = null;
    const { destroyPlayer } = await import('../lib/player/Player');
    destroyPlayer();
});

export {}
