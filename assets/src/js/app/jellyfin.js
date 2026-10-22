// The Jellyfin player page (templates/views/jellyfin/play.html): movi-player
// on /jellyfin/:id/stream with the audio and subtitle picker, plus the up-next prompt and
// the addon subtitles (lib/jellyfin/play.js).
import av from '../lib/av';

let undo = null;

av(async function () {
    const root = this.closest('#jellyfin-player') || this;
    const { initPlayer } = await import('../lib/player/Player');
    await initPlayer(root);
    const video = root.querySelector('.player');
    if (video) {
        const { wirePlayPage, wireAudio, loadAddonSubtitles } = await import('../lib/jellyfin/play');
        const undoPage = wirePlayPage(root, video);
        const undoAudio = wireAudio(root, video);
        // In the background: the addons can take seconds to answer.
        loadAddonSubtitles(root);
        undo = () => { undoAudio(); undoPage(); };
    }
}, async function () {
    if (undo) undo();
    undo = null;
    const { destroyPlayer } = await import('../lib/player/Player');
    destroyPlayer();
});

export {}
