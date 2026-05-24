package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"serv/internal/config"
	"serv/internal/handler"
	"serv/internal/logging"
	"serv/internal/security"
	"serv/internal/tlsconfig"
)

func main() {
	cfg, err := config.Parse()
	if err != nil {
		log.Printf("Error parsing flags: %v", err)
		os.Exit(1)
	}

	dir, err := config.ResolveDir(cfg.Directory)
	if err != nil {
		log.Printf("Error getting working directory: %v", err)
		os.Exit(1)
	}

	logger, closeLog, err := logging.NewLogger(cfg.LogFile)
	if err != nil {
		log.Printf("Error opening log file: %v", err)
		os.Exit(1)
	}
	defer func() {
		if err := closeLog(); err != nil {
			log.Printf("Error closing log file: %v", err)
		}
	}()

	ipChecker, err := security.ParseAllowedIPs(cfg.AllowedIPs)
	if err != nil {
		log.Printf("Error parsing allowed IPs: %v", err)
		os.Exit(1)
	}

	sensitiveFiles, err := security.ResolveSensitiveFiles([]string{cfg.CACertFile, cfg.CertFile, cfg.KeyFile})
	if err != nil {
		log.Printf("Error configuring sensitive TLS paths: %v", err)
		os.Exit(1)
	}

	oneTimeDownloadDirs, err := handler.ResolveOneTimeDownloadDirs(dir, cfg.OneTimeDownloadDirs)
	if err != nil {
		log.Printf("Error configuring one-time download directories: %v", err)
		os.Exit(1)
	}

	oneTimeUploadDirs, err := handler.ResolveOneTimeUploadDirs(dir, cfg.OneTimeUploadDirs)
	if err != nil {
		log.Printf("Error configuring one-time upload directories: %v", err)
		os.Exit(1)
	}
	if err := handler.ValidateOneTimeDirSeparation(oneTimeDownloadDirs, oneTimeUploadDirs); err != nil {
		log.Printf("Error configuring one-time directories: %v", err)
		os.Exit(1)
	}

	credentialFileCache := map[string]basicAuthCredentials{}
	username := resolveCredentialValue(logger, "username", cfg.Username, false, credentialFileCache)
	password := resolveCredentialValue(logger, "password", cfg.Password, true, credentialFileCache)

	h := &handler.Handler{
		Dir:                 dir,
		AllowInsecure:       cfg.AllowInsecure,
		AllowDotFiles:       cfg.AllowDotFiles,
		AllowedIPs:          ipChecker,
		Sensitive:           sensitiveFiles,
		Username:            username,
		Password:            password,
		Headers:             cfg.Headers,
		Redirects:           cfg.Redirects,
		FilterGlobs:         cfg.FilterGlobs,
		RequestChecks:       security.DefaultRequestChecks(),
		EntryFilters:        security.DefaultEntryFilters(),
		UploadEnabled:       cfg.UploadEnabled,
		UploadMaxBytes:      maxUploadBytes(cfg.UploadMaxMB),
		UploadOverwrite:     cfg.UploadOverwrite,
		OneTimeDownloadDirs: oneTimeDownloadDirs,
		OneTimeUploadDirs:   oneTimeUploadDirs,
		Logger:              logger,
	}

	addr := fmt.Sprintf("%s:%d", cfg.ListenIP, cfg.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: h,
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		config, err := tlsconfig.Configure(cfg.CACertFile, cfg.CertFile, cfg.KeyFile, cfg.ClientCertAuth)
		if err != nil {
			log.Printf("Error configuring TLS: %v", err)
			os.Exit(1)
		}
		server.TLSConfig = config
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}

	if err != nil {
		log.Printf("Error starting server: %v", err)
		os.Exit(1)
	}
}

func maxUploadBytes(maxMB int) int64 {
	if maxMB < 0 {
		maxMB = 100
	}
	if maxMB == 0 {
		return 0
	}
	return int64(maxMB) * 1024 * 1024
}

type basicAuthCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func resolveCredentialValue(logger *log.Logger, label string, value string, warnOnPlain bool, fileCache map[string]basicAuthCredentials) string {
	const envPrefix = "env:"
	if strings.HasPrefix(value, envPrefix) {
		key := strings.TrimPrefix(value, envPrefix)
		if key == "" {
			logger.Printf("Warning: %s environment variable name is empty", label)
			return ""
		}
		environmentValue, ok := os.LookupEnv(key)
		if !ok {
			logger.Printf("Warning: %s environment variable %q is not set", label, key)
		}
		return environmentValue
	}

	const filePrefix = "file:"
	if strings.HasPrefix(value, filePrefix) {
		path := strings.TrimPrefix(value, filePrefix)
		if path == "" {
			logger.Printf("Warning: %s file path is empty", label)
			return ""
		}
		creds, ok := fileCache[path]
		if !ok {
			content, err := os.ReadFile(path)
			if err != nil {
				logger.Printf("Warning: failed to read %s credential file %q: %v", label, path, err)
				return ""
			}
			if err := json.Unmarshal(content, &creds); err != nil {
				logger.Printf("Warning: failed to parse %s credential file %q as JSON: %v", label, path, err)
				return ""
			}
			fileCache[path] = creds
		}

		if label == "password" {
			if creds.Password == "" {
				logger.Printf("Warning: password missing in credential file %q", path)
			}
			return creds.Password
		}
		if creds.Username == "" {
			logger.Printf("Warning: username missing in credential file %q", path)
		}
		return creds.Username
	}

	if warnOnPlain && value != "" {
		logger.Printf("Warning: password provided via -password is visible to other users; use env:<VAR> or file:<PATH> instead")
	}

	return value
}
