-- Continue watching for the Jellyfin library (handlers/jellyfin,
-- docs/continue-watching.md). Its own table rather than rows in
-- watch_history: a Jellyfin item is not a torrent resource, and everything
-- that reads watch_history (enrichment, next-episode, the library's watched
-- marks, exports) joins it to torrents. The player reports positions through
-- the same PUT /watch/position (resource_id "jellyfin", path = the item id);
-- the play page records what the strip shows (title, poster, next episode)
-- so the home page never has to ask Jellyfin.
CREATE TABLE public.jellyfin_watch (
	user_id		uuid		NOT NULL,
	item_id		text		NOT NULL,
	title		text		NOT NULL DEFAULT '',
	series		text		NOT NULL DEFAULT '',
	code		text		NOT NULL DEFAULT '',
	image_id	text		NOT NULL DEFAULT '',
	position	real		NOT NULL DEFAULT 0,
	duration	real		NOT NULL DEFAULT 0,
	watched		boolean		NOT NULL DEFAULT false,
	next_item_id	text		NOT NULL DEFAULT '',
	next_title	text		NOT NULL DEFAULT '',
	next_code	text		NOT NULL DEFAULT '',
	created_at	timestamptz	NOT NULL DEFAULT now(),
	updated_at	timestamptz	NOT NULL DEFAULT now(),

	CONSTRAINT jellyfin_watch_pk PRIMARY KEY (user_id, item_id),
	CONSTRAINT jellyfin_watch_user_fk FOREIGN KEY (user_id)
		REFERENCES public."user"(user_id)
		ON DELETE CASCADE
);

CREATE INDEX jellyfin_watch_user_updated_idx ON public.jellyfin_watch (user_id, updated_at DESC);

CREATE TRIGGER update_jellyfin_watch_updated_at
	BEFORE UPDATE ON public.jellyfin_watch
	FOR EACH ROW EXECUTE FUNCTION update_updated_at();
