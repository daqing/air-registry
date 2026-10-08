package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daqing/airway/app/websocket"
	"github.com/daqing/airway/cmd"
	"github.com/daqing/airway/lib/app"
	"github.com/daqing/airway/lib/jsbuild"
	"github.com/daqing/airway/lib/migrate"
	"github.com/daqing/airway/lib/plugin"
	"github.com/daqing/airway/lib/redis_client"
	"github.com/daqing/airway/lib/repo"
	"github.com/daqing/airway/lib/storage"
	"github.com/daqing/airway/lib/utils"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/gc"
	"github.com/daqing/air-registry/app/services/registrycfg"
	"github.com/daqing/air-registry/config"
	airdb "github.com/daqing/air-registry/db"
)

// The project binary starts the HTTP server by default (or via `server`).
// Any other argument is dispatched to the Airway CLI compiled into this
// binary, so project-local code (REPL models, plugins, Go DSL migrations
// imported below) is visible to commands like `go run . repl`.
func main() {
	args, projectDir, err := parseProjectDir(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "air-registry: %v\n\n%s", err, usageText)
		os.Exit(2)
	}

	// `--version` / `-v` print the VERSION file contents directly, without
	// loading .env or any other project setup.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		printVersion()
		return
	}

	// `-d <dir>` pins the project directory: every relative path (the .env
	// file itself, the SQLite database, DATA_DIR, the frontend bundle) then
	// resolves inside <dir>, so the binary can be run from anywhere.
	if projectDir != "" {
		absDir, chdirErr := enterProjectDir(projectDir)
		if chdirErr != nil {
			fmt.Fprintf(os.Stderr, "air-registry: %v\n", chdirErr)
			os.Exit(2)
		}
		projectDir = absDir
	}

	loadEnvFile()

	if projectDir != "" {
		applyProjectDefaults(projectDir)
	}

	if len(args) == 0 || args[0] == "server" {
		runServer()
		return
	}

	if args[0] == "gc" {
		runGC()
		return
	}

	if handled, err := runEmbeddedMigrationCommand(args); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	cmd.Version = versionString()
	cmd.Run(args)
}

// usageText documents the wrapper flags this binary adds on top of the
// Airway CLI it dispatches to.
const usageText = `Usage: air-registry [-d <project-dir>] [command]

  -d, --dir <dir>   use <dir> as the project directory: load its .env and
                    resolve relative paths (SQLite database, DATA_DIR, frontend
                    bundle) inside it, so the command can run from anywhere
  server            start the HTTP server (default)
  gc                reclaim blobs no manifest references
  <command>         any other Airway CLI command (db:migrate, repl, ...)
`

// parseProjectDir extracts the -d/--dir flag from args, returning the
// remaining arguments in order. Both `-d value` and `-d=value` forms are
// accepted; the flag may appear anywhere on the command line so
// `air-registry server -d /srv/registry` works as well as
// `air-registry -d /srv/registry server`.
func parseProjectDir(args []string) (rest []string, dir string, err error) {
	rest = make([]string, 0, len(args))

	setDir := func(value, flag string) error {
		if value == "" {
			return fmt.Errorf("flag %s needs a directory argument", flag)
		}
		if dir != "" {
			return errors.New("project directory given more than once")
		}
		dir = value
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "-d" || arg == "-dir" || arg == "--dir":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("flag %s needs a directory argument", arg)
			}
			if err := setDir(args[i+1], arg); err != nil {
				return nil, "", err
			}
			i++
		case strings.HasPrefix(arg, "-d=") || strings.HasPrefix(arg, "-dir=") || strings.HasPrefix(arg, "--dir="):
			name, value, _ := strings.Cut(arg, "=")
			if err := setDir(value, name); err != nil {
				return nil, "", err
			}
		default:
			rest = append(rest, arg)
		}
	}

	return rest, dir, nil
}

// enterProjectDir resolves dir (expanding a leading ~), verifies it is a
// directory, and chdirs into it so relative paths resolve inside the
// project. It returns the absolute path for later defaulting.
func enterProjectDir(dir string) (string, error) {
	dir = expandHome(strings.TrimSpace(dir))
	if dir == "" {
		return "", errors.New("project directory must not be empty")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("project directory %q: %w", dir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("project directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project directory %q is not a directory", dir)
	}

	if err := os.Chdir(abs); err != nil {
		return "", fmt.Errorf("project directory %q: %w", dir, err)
	}

	return abs, nil
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// applyProjectDefaults points a bare project directory at a SQLite database
// and storage root inside it when .env configures neither, so `-d <dir>`
// works even before .env is filled in. Explicit values always win.
func applyProjectDefaults(dir string) {
	if os.Getenv("AIRWAY_DSN") == "" && os.Getenv("DSN") == "" {
		_ = os.Setenv("AIRWAY_DSN", "sqlite://"+filepath.Join(dir, "registry.db"))
	}
	if os.Getenv("DATA_DIR") == "" && os.Getenv("STORAGE_ROOT") == "" {
		_ = os.Setenv("DATA_DIR", filepath.Join(dir, "data", "storage"))
	}
}

// runGC is the `gc` command: reclaim blobs that no manifest references.
// It uses the same DSN/DATA_DIR env vars as the server.
func runGC() {
	dsn := utils.GetEnvMulti("AIRWAY_DSN", "DSN")
	if len(dsn) == 0 {
		log.Println("gc: DSN is not set")
		os.Exit(1)
	}
	if _, err := repo.SetupDB(dsn); err != nil {
		log.Printf("gc: database setup failed: %v", err)
		os.Exit(3)
	}

	stats, err := gc.Run(blobstore.New(registrycfg.DataDir()))
	if err != nil {
		log.Printf("gc: %v", err)
		os.Exit(1)
	}
	fmt.Printf("gc: kept %d blobs, deleted %d\n", stats.KeptBlobs, stats.DeletedBlobs)
}

func runServer() {
	// .env was already loaded by main (godotenv.Load never overrides existing
	// values), so `LISTEN=0.0.0.0:1988 airway server` wins over a LISTEN in
	// .env and AIRWAY_ENV itself can come from .env.
	appConfig := utils.AppConfig()

	if appConfig.Env == "" {
		log.Println("AIRWAY_ENV is not set")
		os.Exit(1)
	}

	if !appConfig.IsLocal {
		gin.SetMode(gin.ReleaseMode)
	}

	dsn := utils.GetEnvMulti("AIRWAY_DSN", "DSN")

	if len(dsn) > 0 {
		// A `go install`ed binary has no db/migrate on disk, so apply the
		// embedded migrations before serving. Source checkouts keep using
		// `db:migrate` (with its schema snapshot) as before.
		if !airdb.OnDisk() {
			if err := migrate.Run(migrate.Options{DSN: dsn, Migrations: airdb.Migrations()}); err != nil {
				log.Printf("database migration failed: %v", err)
				os.Exit(3)
			}
		}

		if _, setupErr := repo.SetupDB(dsn); setupErr != nil {
			log.Printf("database setup failed: %v", setupErr)
			os.Exit(3)
		}
	}

	redisURL := utils.GetEnvMulti("AIRWAY_REDIS", "REDIS")
	if len(redisURL) > 0 {
		redis_client.Setup(redisURL)
	}

	// In local development the frontend bundle is rebuilt in memory and
	// served with livereload; production serves the embedded dist bundle.
	// A missing vendor directory aborts the boot: the source watcher skips
	// vendor/, so a running server would never pick up a later js:install.
	if appConfig.IsLocal {
		if _, err := jsbuild.StartDefault(".", websocket.Broadcast); err != nil {
			if errors.Is(err, jsbuild.ErrVendorMissing) {
				log.Printf("frontend dev server failed: %v", err)
				os.Exit(6)
			}
			log.Printf("frontend dev server disabled: %v", err)
		}
	}

	if _, err := storage.Setup(storage.FromEnv()); err != nil {
		log.Printf("storage setup failed: %v", err)
		os.Exit(4)
	}

	if err := plugin.BootAll(); err != nil {
		log.Printf("plugin boot failed: %v", err)
		os.Exit(5)
	}

	runApp()
}

// runEmbeddedMigrationCommand handles db:migrate / db:rollback / db:status
// for projects that have no on-disk db/migrate directory, i.e. a project
// directory driven by a `go install`ed binary. It reports handled=false when
// the on-disk migrations exist so the Airway CLI keeps owning them there.
func runEmbeddedMigrationCommand(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "db:migrate", "db:rollback", "db:status":
	default:
		return false, nil
	}

	if airdb.OnDisk() {
		return false, nil
	}

	dsn := utils.GetEnvMulti("AIRWAY_DSN", "DSN")
	if dsn == "" {
		return true, errors.New("database dsn is not configured; set DSN (or AIRWAY_DSN)")
	}

	opts := migrate.Options{DSN: dsn, Migrations: airdb.Migrations()}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "db:migrate":
		target := ""
		if len(args) > 1 {
			target = strings.TrimSpace(args[1])
		}
		return true, migrate.RunTo(opts, target)
	case "db:rollback":
		step := 1
		if len(args) > 1 {
			parsed, parseErr := strconv.Atoi(strings.TrimSpace(args[1]))
			if parseErr != nil || parsed <= 0 {
				return true, fmt.Errorf("db:rollback: invalid step %q", args[1])
			}
			step = parsed
		}
		return true, migrate.Rollback(opts, step)
	default: // db:status
		return true, migrate.Status(opts)
	}
}

// loadEnvFile loads .env from the current working directory, if present.
// Callers may run after `-d <dir>` has chdir'd into the project.
func loadEnvFile() {
	err := godotenv.Load(".env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("Loading env file: .env failed: %v", err)
	}
}

func runApp() {
	a := app.NewApp("Airway", app.WithRoutes(config.Routes, config.HealthRoutes))
	a.Run()
}
