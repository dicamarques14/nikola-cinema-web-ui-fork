// The Jellyfin player page (templates/views/jellyfin/play.html):
// movi-player on /jellyfin/:id/stream, nothing else.
import av from '../lib/av';

av(async function () {
    const root = this.closest('#jellyfin-player') || this;
    const { initPlayer } = await import('../lib/player/Player');
    await initPlayer(root);
}, async function () {
    const { destroyPlayer } = await import('../lib/player/Player');
    destroyPlayer();
});

export {}
