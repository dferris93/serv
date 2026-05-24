package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port                int
	ListenIP            string
	LogFile             string
	Directory           string
	CACertFile          string
	CertFile            string
	KeyFile             string
	ClientCertAuth      bool
	Username            string
	Password            string
	AllowInsecure       bool
	AllowDotFiles       bool
	AllowedIPs          []string
	Headers             map[string]string
	Redirects           map[string]string
	FilterGlobs         []string
	UploadEnabled       bool
	UploadMaxMB         int
	UploadOverwrite     bool
	OneTimeDownloadDirs []string
	OneTimeUploadDirs   []string
}

type multiValueFlag []string

func (m *multiValueFlag) String() string {
	return fmt.Sprintf("%v", *m)
}

func (m *multiValueFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func makeMap(values multiValueFlag) map[string]string {
	m := make(map[string]string)
	for _, header := range values {
		parts := strings.SplitN(header, ":", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}

func splitCommaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func Parse() (Config, error) {
	flag.CommandLine.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "serv is a lightweight HTTP/HTTPS file server for sharing a directory.")
		fmt.Fprintln(out, "It supports TLS/mTLS, basic auth with .htaccess overrides, IP allowlists,")
		fmt.Fprintln(out, "custom headers/redirects, and a custom directory listing with")
		fmt.Fprintln(out, "secure-by-default file access.")
		fmt.Fprintln(out, "")
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Htaccess:")
		fmt.Fprintln(out, "  If a .htaccess file exists in a directory (or any parent up to the served root),")
		fmt.Fprintln(out, "  its username/password override -username/-password for that subtree.")
		fmt.Fprintln(out, "  Format supports username/password entries with ':' or '=' (e.g. username: admin).")
		fmt.Fprintln(out, "  .htaccess is never served to clients.")
	}

	port := flag.Int("port", 8889, "Port to listen on")
	listenIP := flag.String("ip", "127.0.0.1", "IP to listen on")
	logFile := flag.String("log", "", "Log file path (empty for stdout)")
	directory := flag.String("dir", ".", "Directory to serve")
	CACertFile := flag.String("cacert", "", "Path to CA certificate file for TLS (optional).")
	certFile := flag.String("cert", "", "Path to host certificate file for TLS (optional)")
	keyFile := flag.String("key", "", "Path to private key file for TLS (optional)")
	clientCertAuth := flag.Bool("mtls", false, "Require client certificate for TLS (optional)")
	username := flag.String("username", "", "Username for basic auth (optional). Supports env:VAR or file:/path/to/file")
	password := flag.String("password", "", "Password for basic auth (optional). Supports env:VAR or file:/path/to/file")
	allowInsecure := flag.Bool("insecure", false, "Allow insecure symlinks and files (optional)")
	allowDotFiles := flag.Bool("allowdotfiles", false, "Allow files starting with a dot (optional)")
	allowedIPs := flag.String("allowedips", "", "Comma separated list of allowed IPs (optional)")
	uploadEnabled := flag.Bool("upload", false, "Enable browser uploads (optional)")
	uploadMaxMB := flag.Int("uploadmaxmb", 100, "Maximum upload request size in MB (optional, 0 for unlimited)")
	uploadOverwrite := flag.Bool("uploadoverwrite", false, "Allow uploaded files to overwrite existing files (optional)")

	var headersFlag multiValueFlag
	flag.Var(&headersFlag, "header", "HTTP headers to include in the response. Can specify multiple.")

	var redirectsFlag multiValueFlag
	flag.Var(&redirectsFlag, "redirect", "Redirects to add. Can specify multiple.")

	var filtersFlag multiValueFlag
	flag.Var(&filtersFlag, "filter", "Glob patterns to hide from directory listings and block direct access. Can specify multiple.")

	var oneTimeDownloadDirsFlag multiValueFlag
	flag.Var(&oneTimeDownloadDirsFlag, "otd", "Directory whose files are deleted after one successful GET. Can specify multiple.")

	var oneTimeUploadDirsFlag multiValueFlag
	flag.Var(&oneTimeUploadDirsFlag, "otu", "Directory that accepts one-time uploads and blocks downloads. Can specify multiple.")

	flag.Parse()

	cfg := Config{
		Port:                *port,
		ListenIP:            *listenIP,
		LogFile:             *logFile,
		Directory:           *directory,
		CACertFile:          *CACertFile,
		CertFile:            *certFile,
		KeyFile:             *keyFile,
		ClientCertAuth:      *clientCertAuth,
		Username:            *username,
		Password:            *password,
		AllowInsecure:       *allowInsecure,
		AllowDotFiles:       *allowDotFiles,
		AllowedIPs:          splitCommaList(*allowedIPs),
		Headers:             makeMap(headersFlag),
		Redirects:           makeMap(redirectsFlag),
		FilterGlobs:         filtersFlag,
		UploadEnabled:       *uploadEnabled,
		UploadMaxMB:         *uploadMaxMB,
		UploadOverwrite:     *uploadOverwrite,
		OneTimeDownloadDirs: oneTimeDownloadDirsFlag,
		OneTimeUploadDirs:   oneTimeUploadDirsFlag,
	}

	return cfg, nil
}

func ResolveDir(dir string) (string, error) {
	cleaned := filepath.Clean(dir)
	if cleaned == "." || cleaned == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return cwd, nil
	}
	return dir, nil
}
