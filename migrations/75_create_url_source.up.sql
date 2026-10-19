-- A file a Stremio addon serves over plain HTTP(S) instead of a torrent
-- (docs/url_streams.md). The row is what the server needs to fetch it again:
-- the addon it came from and the stream request (content_type/content_id) so
-- an expired link can be re-resolved, and the link itself with the headers
-- the addon asked for. url and proxy_headers are credentials (signed links,
-- CDN cookies) and never leave the server. info_hash is set once the import
-- has turned the file into a torrent.
CREATE TABLE public.url_source (
	url_source_id uuid DEFAULT uuid_generate_v4() NOT NULL,
	user_id uuid NOT NULL,
	stremio_addon_url_id uuid,
	content_type text NOT NULL,
	content_id text NOT NULL,
	title text DEFAULT '' NOT NULL,
	filename text NOT NULL,
	size bigint DEFAULT 0 NOT NULL,
	url text NOT NULL,
	proxy_headers jsonb,
	status text DEFAULT 'new' NOT NULL,
	error text DEFAULT '' NOT NULL,
	info_hash text,
	staging_path text DEFAULT '' NOT NULL,
	created_at timestamptz DEFAULT now() NOT NULL,
	updated_at timestamptz DEFAULT now() NOT NULL,
	CONSTRAINT url_source_pk PRIMARY KEY (url_source_id),
	CONSTRAINT url_source_user_fk FOREIGN KEY (user_id)
		REFERENCES public."user" (user_id) ON DELETE CASCADE,
	CONSTRAINT url_source_addon_fk FOREIGN KEY (stremio_addon_url_id)
		REFERENCES public.stremio_addon_url (stremio_addon_url_id) ON DELETE SET NULL
);

CREATE INDEX url_source_user_content_idx ON public.url_source (user_id, content_id);

create trigger update_updated_at before
update
    on
    public.url_source for each row execute function update_updated_at();
