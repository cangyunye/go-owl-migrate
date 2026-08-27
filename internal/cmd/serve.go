package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/paths"
	"github.com/cangyunye/go-owl-migrate/internal/server/master"
	"github.com/cangyunye/go-owl-migrate/internal/server/serve"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func serveCmd() *cobra.Command {
	var (
		port       int
		host       string
		masterPort int
		tempDir    string
		dbPath     string
		configOut  string
		configDir  string
		token      string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the owl-migrate web server",
		Long: `Starts the owl-migrate web UI server.

The server provides a browser-based interface for all migration operations:
configuration, DDL generation, data export/import, and full migration pipeline
with real-time progress monitoring via WebSocket.

No authentication is required — intended for local or trusted-network use.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				dbPath = paths.DBPath()
			}
			if configOut == "" {
				configOut = paths.ConfigFile()
			}
			if configDir == "" {
				configDir = paths.ConfigLibraryDir()
			}
			if token == "" {
				token = os.Getenv("OWL_MIGRATE_TOKEN")
			}
			if err := requireBindHost(host, token); err != nil {
				return err
			}
			os.MkdirAll(tempDir, 0755)
			os.MkdirAll(filepath.Dir(dbPath), 0755)
			os.MkdirAll(configDir, 0755)

			lockPath := paths.ServeLockPath()
			if err := acquireServeLock(lockPath); err != nil {
				return err
			}
			defer releaseServeLock(lockPath)

			store, err := service.NewJobStore(dbPath)
			if err != nil {
				return fmt.Errorf("open job store: %w", err)
			}
			defer store.Close()

			interrupted, err := store.MarkRunningAsInterrupted()
			if err != nil {
				return fmt.Errorf("mark interrupted: %w", err)
			}
			if interrupted > 0 {
				fmt.Printf("Marked %d previously running jobs as interrupted\n", interrupted)
			}

			spawner := &execSpawner{tempDir: tempDir}
			m := master.New(master.Config{
				Store:   store,
				Spawner: spawner,
				TempDir: tempDir,
				DBPath:  dbPath,
			})

			ipcPort := masterPort
			if ipcPort == 0 {
				ipcPort, err = selectIPCPort()
				if err != nil {
					return fmt.Errorf("select IPC port: %w", err)
				}
			}

			ipcAddr := fmt.Sprintf("127.0.0.1:%d", ipcPort)
			ipcServer := &http.Server{Addr: ipcAddr, Handler: m.Handler()}
			go func() {
				fmt.Printf("Master IPC listening on %s\n", ipcAddr)
				if err := ipcServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "IPC server error: %v\n", err)
				}
			}()

			srv := serve.NewServer(serve.Config{
				Store:          store,
				MasterURL:      fmt.Sprintf("http://%s", ipcAddr),
				ConfigPath:     configOut,
				TempDir:        tempDir,
				ConfigDir:      configDir,
				DataSourcesDir: paths.DataSourcesDir(),
				Token:          token,
			})

			serveAddr := fmt.Sprintf("%s:%d", host, port)
			httpServer := &http.Server{Addr: serveAddr, Handler: srv.Handler()}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			go func() {
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						m.WriteHeartbeat()
					}
				}
			}()

			go func() {
				fmt.Printf("owl-migrate web UI: http://%s\n", serveAddr)
				if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
					stop()
				}
			}()

			<-ctx.Done()
			fmt.Println("\nShutting down...")

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			httpServer.Shutdown(shutdownCtx)
			ipcServer.Shutdown(shutdownCtx)

			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "web UI listen port")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "web UI listen address")
	cmd.Flags().IntVar(&masterPort, "master-ipc-port", 0, "master IPC port (0=auto-select)")
	cmd.Flags().StringVar(&tempDir, "temp-dir", "./output/temp/", "temp directory for jobs and exports")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: ~/.owl/migrate/owl-migrate.db)")
	cmd.Flags().StringVar(&configOut, "config-out", "", "where saved configs are written (default: ~/.owl/migrate/migrate.yaml)")
	cmd.Flags().StringVar(&configDir, "config-dir", "", "directory for the reusable config library (default: ~/.owl/migrate/configs/library/)")
	cmd.Flags().StringVar(&token, "token", "", "auth token (also OWL_MIGRATE_TOKEN); required to bind non-loopback")

	return cmd
}

func selectIPCPort() (int, error) {
	preferred := []int{25430, 25431, 25432, 25433, 25434, 25435, 25436, 25437, 25438, 25439}
	ranges := [][2]int{{25400, 25499}, {25000, 25999}}

	for _, port := range preferred {
		if isPortAvailable(port) {
			return port, nil
		}
	}
	for _, r := range ranges {
		for port := r[0]; port <= r[1]; port++ {
			if isPortAvailable(port) {
				return port, nil
			}
		}
	}
	for port := 26000; port < 27000; port++ {
		if isPortAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available IPC port found, specify with --master-ipc-port")
}

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

type execSpawner struct {
	tempDir string
}

func (s *execSpawner) Spawn(req master.SpawnRequest) (int, func() error, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, nil, fmt.Errorf("find executable: %w", err)
	}

	// "export" is a parent command; the data export lives at "export data".
	var args []string
	switch req.JobType {
	case "export":
		args = []string{"export", "data"}
	case "import":
		args = []string{"import"}
	default: // migrate
		args = []string{"migrate"}
	}

	args = append(args,
		"--config", req.ConfigPath,
		"--progress-db", req.DBPath,
		"--job-id", req.JobID,
		"--parent-pid", fmt.Sprintf("%d", req.ParentPID),
	)

	// Command-specific flags. --temp-dir only exists on migrate. Export defaults
	// to ./output/data/ (shared with import's source_dir so the two chain).
	switch req.JobType {
	case "migrate":
		if req.TempDir != "" {
			args = append(args, "--temp-dir", req.TempDir)
		}
		if req.Resume {
			args = append(args, "--resume")
		}
		if req.Mode == "sql-out" {
			args = append(args, "--sql-out", filepath.Join(req.TempDir, "insert"))
		}
		if req.SkipDDL {
			args = append(args, "--skip-ddl")
		}
		if req.ContinueOnError {
			args = append(args, "--continue-on-error")
		}
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("start worker: %w", err)
	}

	return cmd.Process.Pid, cmd.Wait, nil
}
