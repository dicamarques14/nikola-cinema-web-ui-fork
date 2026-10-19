package urlsource

import "github.com/urfave/cli"

const (
	EnabledFlag     = "url-streams-enabled"
	ImportDirFlag   = "url-import-dir"
	MaxBytesFlag    = "url-import-max-bytes"
	SeedBaseURLFlag = "url-seed-base-url"
	ConcurrencyFlag = "url-import-concurrency"
)

// RegisterFlags adds the direct-URL stream settings (docs/url_streams.md).
func RegisterFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.BoolFlag{
			Name:   EnabledFlag,
			Usage:  "play Stremio addon streams that are plain HTTP(S) files (proxied through this server)",
			EnvVar: "URL_STREAMS_ENABLED",
		},
		cli.StringFlag{
			Name:   ImportDirFlag,
			Usage:  "staging directory for imported direct-URL files",
			Value:  "/data/.webtor/urlimport",
			EnvVar: "URL_IMPORT_DIR",
		},
		cli.Int64Flag{
			Name:   MaxBytesFlag,
			Usage:  "size cap of the staging directory in bytes; oldest files are evicted first",
			Value:  50 << 30,
			EnvVar: "URL_IMPORT_MAX_BYTES",
		},
		cli.StringFlag{
			Name:   SeedBaseURLFlag,
			Usage:  "base URL at which torrent-web-seeder reaches this web-ui (e.g. http://127.0.0.1:8097); empty disables the import into a torrent",
			EnvVar: "URL_SEED_BASE_URL",
		},
		cli.IntFlag{
			Name:   ConcurrencyFlag,
			Usage:  "how many direct-URL imports run at once",
			Value:  2,
			EnvVar: "URL_IMPORT_CONCURRENCY",
		},
	)
}
