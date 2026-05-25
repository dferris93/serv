# Simple HTTP Based Go File server

This is a simple go program that will serve files in a specified directory over HTTP.
Compared to Caddy, `serv` is intentionally narrow: it focuses on lightweight static file serving with explicit flags, while Caddy is a full-featured web server and reverse proxy with automatic HTTPS and broader app routing/config support.

## Getting Started

### Prerequisites

To run this program, you'll need to have [Go](https://golang.org/dl/) installed on your computer.

I used go 1.18, which is the Ubuntu 20.04 default.

### Installation

```
git clone https://github.com/dferris93/serv.git
cd serv
make
```

### Running
By default, serv will run on 127.0.0.1:8889

Once, the program is running, point your browser to http://127.0.0.1:8889

## Useage

```
  -allowedips string
    	Comma separated list of allowed IPs (optional)
  -allowdotfiles
    	Allow files starting with a dot (optional)
  -insecure
    	Allow insecure symlinks and files (optional)
  -cacert string
    	Path to CA certificate file for TLS (optional)
  -cert string
    	Path to CA certificate file for TLS (optional)
  -mtls
    	Require client certificate for TLS (optional)
  -dir string
    	Directory to serve (default ".")
  -filter value
    	Glob patterns to hide from directory listings and block direct access. Can specify multiple.
  -header value
    	HTTP headers to include in the response. Can specify multiple.
  -ip string
    	IP to listen on (default "127.0.0.1")
  -key string
    	Path to private key file for TLS (optional)
  -log string
    	Log file path (empty for stdout)
  -otd value
        Directory whose files are deleted after one successful GET. Can specify multiple.
  -otu value
        Directory that accepts one-time uploads and blocks downloads. Can specify multiple.
  -password string
    	Password for basic auth (optional). Use env:VAR to read from an environment variable or file:/path/to/file to read JSON credentials.
  -port int
    	Port to listen on (default 8889)
  -redirect value
    	Redirects to add. Can specify multiple.
  -upload
    	Enable browser uploads (optional)
  -uploadmaxmb int
    	Maximum upload request size in MB (optional, 0 for unlimited) (default 100)
  -uploadoverwrite
    	Allow uploaded files to overwrite existing files (optional)
  -username string
    	Username for basic auth (optional). Use env:VAR to read from an environment variable or file:/path/to/file to read JSON credentials.

```

## Example

* Serve files in $HOME/public_html to the entire world on port 8080

```
./serv -dir $HOME/public_html -ip 0.0.0.0 -port 8080 
```

* Serve files in $HOME/public_html to the entire world on port 8080 and log to $HOME/access_log

```
./serv -dir $HOME/public_html -ip 0.0.0.0 -port 8080 -log $HOME/access_log
```

* Make a self signed TLS key pair and serve with it
```
openssl genpkey -algorithm RSA -out server.key
openssl req -new -x509 -key server.key -out server.crt -days 365

<answer the questions needed for the CSR>

./serv -cacert server.crt -key server.key -port 8443

```

* Use http basic auth
```
./serv -username admin -password admin 
```

* Use http basic auth with environment variables
```
export SERV_USER=admin
export SERV_PASS=admin
./serv -username env:SERV_USER -password env:SERV_PASS
```

* Use http basic auth with a JSON credentials file
```
cat >/tmp/serv-creds.json <<'EOF'
{"username":"admin","password":"admin"}
EOF
./serv -username file:/tmp/serv-creds.json -password file:/tmp/serv-creds.json
```

* Use http basic auth with .htaccess (per-directory override)
```
echo "username: admin" > /path/to/served/.htaccess
echo "password: admin" >> /path/to/served/.htaccess
./serv -dir /path/to/served
```
* If a `.htaccess` file exists in a directory (or any parent up to the served root), its credentials override the CLI `-username`/`-password` for that subtree.

* Use http basic auth with TLS
```
./serv -cacert server.crt -key server.key -username admin -password admin
```

* Delete files after one successful download
```
mkdir -p /path/to/served/once
./serv -dir /path/to/served -otd once
```
Files under `once` are deleted after a successful full `GET`. `HEAD`, conditional `304`, and range `206` responses do not delete the file.

* Accept uploads without allowing downloads
```
mkdir -p /path/to/served/drop
./serv -dir /path/to/served -otu drop
```
Directories configured with `-otu` show the upload UI, hide all entries, block direct file downloads, store uploads as `name_<sha256>.ext` (or `name_<sha256>` when there is no extension), and reject uploads whose SHA-256 already exists in the directory.

* TLS cert auth
```
 openssl genpkey -algorithm RSA -out ca-key.pem
 openssl req -new -x509 -key ca-key.pem -out ca-cert.pem -days 3650 -subj "/CN=My CA"
 openssl genpkey -algorithm RSA -out server-key.pem
 openssl req -new -key server-key.pem -out server-csr.pem -subj "/CN=localhost"
 openssl x509 -req -in server-csr.pem -CA ca-cert.pem -CAkey ca-key.pem -CAcreateserial -out server-cert.pem -days 365
 openssl genpkey -algorithm RSA -out client-key.pem
 openssl req -new -key client-key.pem -out client-csr.pem -subj "/CN=Client"
 openssl x509 -req -in client-csr.pem -CA

 ./serv -cacert ca-cert.pem -cert server-cert.pem -key server-key.pem -mtls

#And then on the client
curl https://localhost:8889/ --cert client-cert.pem --key client-key.pem  --cacert ca-cert.pem

```

* Setting Headers
```
./serv -header 'X-Test-Header:Value' -header 'X-Another-Header:Value2' 
```

* IP ACLs
```
./serv -allowedips 192.168.1.0/24,10.10.10.1,172.16.5.0/22
```

* Redirects
```
./serv -redirect '/g:https://www.google.com' -redirect '/a:https://www.amazon.com'
```

* Filter entries from directory listings
```
./serv -filter '*.log' -filter 'node_modules' -filter 'private/*'
```

* Enable browser drag-and-drop uploads (multi-file)
```
./serv -upload
```

* Enable uploads with a 250MB limit and allow overwrites
```
./serv -upload -uploadmaxmb 250 -uploadoverwrite
```

* Enable uploads with no request size limit
```
./serv -upload -uploadmaxmb 0
```

* Upload a file with `curl` (filename is in the URL)
```
mkdir -p /path/to/served/uploads
curl -X POST --data-binary @./example.txt http://127.0.0.1:8889/uploads/example.txt
```

* Upload multiple files with `curl`
```
curl -X POST --data-binary @./example.txt http://127.0.0.1:8889/uploads/example.txt
curl -X POST --data-binary @./image.png  http://127.0.0.1:8889/uploads/image.png
```

## Notes

* I have not tested TLS with an intermediary certificate chain at all, although it should work the same way it works with nginx where you have to order your ca certificates properly in the cacert file.
* TLS is locked to a minimum of version 1.2.  I really don't recommend changing this.
* By default serv will not follow symlinks outside of the directory tree.
* serv blocks hardlinks by default (use `-insecure` to bypass).
* By default serv will not allow access to dot files (use `-allowdotfiles` to allow them)
* If a `.htaccess` file is present, it is never served to clients, even with `-insecure` or `-allowdotfiles`
* Requests for `.htaccess` or configured TLS cert/key/CA files return 404 (including `.htaccess` parse errors). These files are never listed or served, even if they live in the served tree or are symlinked or hardlinked into it.
* serv will look for an index.html file, if it isn't found, it will serve the entire directory.
* `-filter` patterns also block direct access by URL (404).
* Uploads are disabled by default; enable with `-upload`.
* `-otu` enables uploads only for the configured write-only directories, even when `-upload` is not set.
* Uploads respect ACLs and security rules, including auth/IP restrictions, `-filter` patterns, dotfile policy, and sensitive file protections.
* Upload requests can target a directory with multipart form data, or a specific file path via `POST /path/to/<filename>`.
* `-otu` directories store uploads with a SHA-256 suffix and reject duplicate file content, even when `-uploadoverwrite` is set.
* `.htaccess` uploads are always blocked.
* custom headers will only be set if the request is successful
* Access logs use Common Log Format (CLF):
```
host - - [time] "method request-target HTTP/Version" responseCode bytesSent
```
* The `host` field is the client IP from `X-Forwarded-For` (first value) or `X-Real-IP` when present; otherwise the socket remote address is used.

serv is secure by default, but you have to be intelligent.  If you serve your private ssh keys (or other private data) to the Internet, that's on you.
