package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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
		port        int
		host        string
		masterPort  int
		tempDir     string
		dbPath      string
		configOut   string
		configDir   string
		token       string
		openBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the owl-migrate web server",
		Long: `Starts the owl-migrate web UI server.

The server provides a browser-based interface for all migration operations:
configuration, DDL generation, data export/import, and full migration pipeline
with real-time progress monitoring via WebSocket.

No authentication is required — intended for local or trusted-network use.`,
		Example: `  # Local use (default: http://127.0.0.1:8080, opens nothing)
  owl-migrate serve

  # Start and open the browser automatically
  owl-migrate serve --open

  # Trusted-network use with a bearer token (UI will prompt for it)
  OWL_MIGRATE_TOKEN=s3cret owl-migrate serve --host 0.0.0.0 --port 8080`,
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
				// The master IPC endpoint is an internal implementation detail
				// (the web UI talks to it); startup stays quiet about it.
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
			// Bind before printing the banner so the URL is live when shown.
			ln, err := net.Listen("tcp", serveAddr)
			if err != nil {
				return fmt.Errorf("listen %s: %w", serveAddr, err)
			}
			httpServer := &http.Server{Handler: srv.Handler()}

			// 控制台按实际绑定地址显示（--host 0.0.0.0 就显示 0.0.0.0），
			// 不再一律替换成 127.0.0.1——否则用户看不出真正监听的是哪些接口。
			// 浏览器无法访问通配地址，因此自动打开时才回退到回环地址。
			displayHost := host
			if displayHost == "" {
				displayHost = "0.0.0.0"
			}
			uiURL := fmt.Sprintf("http://%s:%d", displayHost, port)
			browseHost := displayHost
			if browseHost == "0.0.0.0" || browseHost == "::" {
				browseHost = "127.0.0.1"
			}
			browseURL := fmt.Sprintf("http://%s:%d", browseHost, port)
			printServeBanner(uiURL, browseURL, displayHost == "0.0.0.0" || displayHost == "::", port, token != "")
			if openBrowser {
				if err := openInBrowser(browseURL); err != nil {
					fmt.Fprintf(os.Stderr, "could not open browser: %v (open %s manually)\n", err, browseURL)
				}
			}

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
			go srv.CleanupLoop(ctx)

			go func() {
				if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
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
	cmd.Flags().BoolVar(&openBrowser, "open", false, "open the web UI in the default browser after startup")

	return cmd
}

// printServeBanner writes the user-facing startup summary: what to open, where
// the docs live, and how to stop. Keep it free of internal details (the master
// IPC endpoint is not user-facing). The bind URL is shown verbatim so --host
// is reflected; when bound to a wildcard address the machine's LAN IPv4 URLs
// are listed too (0.0.0.0 itself is not directly browsable). browseURL is the
// loopback form used for the docs link.
func printServeBanner(uiURL, browseURL string, wildcard bool, port int, tokenEnabled bool) {
	authLine := "token auth disabled — local/trusted network only (set --token to require a bearer token)"
	if tokenEnabled {
		authLine = "token auth enabled — the UI will prompt for the bearer token"
	}
	fmt.Printf("owl-migrate web UI: %s\n", uiURL)
	if wildcard {
		for _, ip := range lanIPv4s() {
			fmt.Printf("  LAN:   http://%s:%d\n", ip, port)
		}
	}
	fmt.Printf("  docs:  %s/docs\n", browseURL)
	fmt.Printf("  auth:  %s\n", authLine)
	fmt.Printf("  stop:  Ctrl+C\n")
}

// lanIPv4s returns the machine's non-loopback IPv4 addresses (best-effort).
func lanIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out
}

// openInBrowser opens url in the default browser, best-effort per platform.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
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

	// Command-specific flags. --temp-dir only exists on migrate. Export writes
	// into the job's OWN directory (<jobDir>/data) so the job-detail artifact
	// panel never mixes in other jobs' files; standalone CLI export keeps its
	// shared ./output/data/ default.
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
	case "export":
		if req.TempDir != "" {
			args = append(args, "--output", filepath.Join(req.TempDir, "data"))
		}
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	if req.Stderr != nil {
		cmd.Stderr = io.MultiWriter(os.Stderr, req.Stderr)
	} else {
		cmd.Stderr = os.Stderr
	}
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("start worker: %w", err)
	}

	return cmd.Process.Pid, cmd.Wait, nil
}
