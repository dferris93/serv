#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
SITE_DIR="$TMP_DIR/site"
TLS_DIR="$SITE_DIR/tls"
LOG_FILE="$TMP_DIR/serv.log"
BIN_PATH="${BIN_PATH:-$TMP_DIR/serv}"
PID_FILE="${PID_FILE:-/tmp/serv-integration.pid}"
SERVER_PID=""
SERVER_PIDS=()
SERVER_PORT=""
UPLOADED_TEST_FILES=()

cleanup() {
  stop_all_servers
  cleanup_stale_pid
  cleanup_uploaded_test_files
  if [[ -f "${PID_FILE}" ]]; then
    rm -f "${PID_FILE}"
  fi
  if [[ "${KEEP_TMP:-0}" != "1" ]]; then
    rm -rf "${TMP_DIR}"
  fi
}
trap cleanup EXIT INT TERM

log() {
  echo "==> $*"
}

fail() {
  echo "error: $*" >&2
  exit 1
}

register_uploaded_test_file() {
  UPLOADED_TEST_FILES+=("$1")
}

cleanup_uploaded_test_files() {
  local file
  for file in "${UPLOADED_TEST_FILES[@]}"; do
    if [[ -n "${file}" && -e "${file}" ]]; then
      rm -f "${file}" || true
    fi
  done
  UPLOADED_TEST_FILES=()
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

build_binary() {
  log "building serv"
  (cd "${ROOT_DIR}" && go build -o "${BIN_PATH}" .)
}

setup_site() {
  mkdir -p "${SITE_DIR}/protected"
  mkdir -p "${SITE_DIR}/broken"
  mkdir -p "${SITE_DIR}/subauth"
  mkdir -p "${SITE_DIR}/uploads"
  mkdir -p "${SITE_DIR}/otd"
  mkdir -p "${SITE_DIR}/drop"
  mkdir -p "${TMP_DIR}/outside"
  echo "hello" >"${SITE_DIR}/hello.txt"
  echo "secret" >"${SITE_DIR}/.hidden"
  echo "log" >"${SITE_DIR}/app.log"
  echo "hardlink" >"${SITE_DIR}/hardlink-source.txt"
  echo "protected" >"${SITE_DIR}/protected/secret.txt"
  echo "broken" >"${SITE_DIR}/broken/file.txt"
  echo "subauth" >"${SITE_DIR}/subauth/secret.txt"
  echo "download once" >"${SITE_DIR}/otd/once.txt"
  echo "existing upload" >"${SITE_DIR}/drop/existing.txt"
  cat >"${SITE_DIR}/protected/.htaccess" <<'EOF'
username: ht
password: pass
EOF
  cat >"${SITE_DIR}/subauth/.htaccess" <<'EOF'
username: sub
password: dir
EOF
  cat >"${SITE_DIR}/broken/.htaccess" <<'EOF'
username: only
EOF
  echo "outside" >"${TMP_DIR}/outside/secret.txt"
  if ln -s "${TMP_DIR}/outside/secret.txt" "${SITE_DIR}/escape.txt" 2>/dev/null; then
    SYMLINK_READY=1
  else
    SYMLINK_READY=0
  fi
  if ln "${SITE_DIR}/hardlink-source.txt" "${SITE_DIR}/hardlink.txt" 2>/dev/null; then
    HARDLINK_READY=1
  else
    HARDLINK_READY=0
  fi
}

generate_certs() {
  mkdir -p "${TLS_DIR}"

  cat >"${TMP_DIR}/server.cnf" <<'EOF'
[ req ]
distinguished_name = dn
req_extensions = req_ext
prompt = no

[ dn ]
CN = 127.0.0.1

[ req_ext ]
subjectAltName = @alt_names
extendedKeyUsage = serverAuth

[ alt_names ]
DNS.1 = localhost
IP.1 = 127.0.0.1
EOF

  cat >"${TMP_DIR}/client.cnf" <<'EOF'
[ req ]
distinguished_name = dn
req_extensions = req_ext
prompt = no

[ dn ]
CN = serv test client

[ req_ext ]
extendedKeyUsage = clientAuth
EOF

  openssl genrsa -out "${TLS_DIR}/ca.key" 2048 >/dev/null 2>&1
  openssl req -x509 -new -nodes -key "${TLS_DIR}/ca.key" -sha256 -days 1 \
    -subj "/CN=serv test ca" -out "${TLS_DIR}/ca.pem" >/dev/null 2>&1

  openssl genrsa -out "${TLS_DIR}/server.key" 2048 >/dev/null 2>&1
  openssl req -new -key "${TLS_DIR}/server.key" -out "${TLS_DIR}/server.csr" \
    -config "${TMP_DIR}/server.cnf" >/dev/null 2>&1
  openssl x509 -req -in "${TLS_DIR}/server.csr" -CA "${TLS_DIR}/ca.pem" \
    -CAkey "${TLS_DIR}/ca.key" -CAcreateserial -out "${TLS_DIR}/server.pem" \
    -days 1 -sha256 -extfile "${TMP_DIR}/server.cnf" -extensions req_ext >/dev/null 2>&1

  openssl genrsa -out "${TLS_DIR}/client.key" 2048 >/dev/null 2>&1
  openssl req -new -key "${TLS_DIR}/client.key" -out "${TLS_DIR}/client.csr" \
    -config "${TMP_DIR}/client.cnf" >/dev/null 2>&1
  openssl x509 -req -in "${TLS_DIR}/client.csr" -CA "${TLS_DIR}/ca.pem" \
    -CAkey "${TLS_DIR}/ca.key" -CAcreateserial -out "${TLS_DIR}/client.pem" \
    -days 1 -sha256 -extfile "${TMP_DIR}/client.cnf" -extensions req_ext >/dev/null 2>&1
}

pick_port() {
  for _ in {1..20}; do
    PORT="$((RANDOM % 10000 + 40000))"
    if command -v lsof >/dev/null 2>&1; then
      if ! lsof -iTCP:"${PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "${PORT}"
        return
      fi
    else
      echo "${PORT}"
      return
    fi
  done
  fail "unable to pick a free port"
}

start_server() {
  local port="$1"
  shift
  SERV_INTEGRATION=1 "${BIN_PATH}" -ip 127.0.0.1 -port "${port}" -dir "${SITE_DIR}" "$@" >"${LOG_FILE}" 2>&1 &
  SERVER_PID="$!"
  SERVER_PIDS+=("${SERVER_PID}")
  printf "%s\n" "${SERVER_PIDS[@]}" >"${PID_FILE}"
}

start_server_retry() {
  local attempts=20
  local port
  for _ in $(seq 1 "${attempts}"); do
    port="$(pick_port)"
    start_server "${port}" "$@"
    sleep 0.1
    if kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
      SERVER_PORT="${port}"
      return 0
    fi
    if grep -qi "address already in use" "${LOG_FILE}"; then
      stop_server
      continue
    fi
    fail "server failed to start on port ${port}. Logs:\n$(cat "${LOG_FILE}")"
  done
  fail "unable to start server after ${attempts} attempts"
}

stop_server() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
  SERVER_PID=""
  rewrite_pid_file
}

stop_all_servers() {
  local pid
  for pid in "${SERVER_PIDS[@]}"; do
    if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
      kill "${pid}" >/dev/null 2>&1 || true
      wait "${pid}" >/dev/null 2>&1 || true
    fi
  done
  SERVER_PIDS=()
  SERVER_PID=""
  if [[ -f "${PID_FILE}" ]]; then
    rm -f "${PID_FILE}"
  fi
}

rewrite_pid_file() {
  local live=()
  local pid
  for pid in "${SERVER_PIDS[@]}"; do
    if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
      live+=("${pid}")
    fi
  done
  SERVER_PIDS=("${live[@]}")
  if [[ "${#SERVER_PIDS[@]}" -gt 0 ]]; then
    printf "%s\n" "${SERVER_PIDS[@]}" >"${PID_FILE}"
  elif [[ -f "${PID_FILE}" ]]; then
    rm -f "${PID_FILE}"
  fi
}

wait_for_url() {
  local url="$1"
  shift
  local deadline=$((SECONDS + 5))
  while ((SECONDS < deadline)); do
    if curl --fail --silent --show-error --connect-timeout 1 "$@" "${url}" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  fail "server did not become ready for ${url}. Logs:\n$(cat "${LOG_FILE}")"
}

expect_status() {
  local expected="$1"
  local url="$2"
  shift 2
  local code
  code="$(curl --silent --output /dev/null --write-out "%{http_code}" "$@" "${url}" || true)"
  if [[ -z "${code}" || "${code}" == "000" ]]; then
    fail "request failed for ${url}"
  fi
  if [[ "${code}" != "${expected}" ]]; then
    fail "expected HTTP ${expected} for ${url}, got ${code}"
  fi
}

wait_for_status() {
  local expected="$1"
  local url="$2"
  shift 2
  local deadline=$((SECONDS + 5))
  while ((SECONDS < deadline)); do
    local code
    code="$(curl --silent --output /dev/null --write-out "%{http_code}" "$@" "${url}" || true)"
    if [[ "${code}" == "${expected}" ]]; then
      return 0
    fi
    if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  fail "server did not return ${expected} for ${url}. Logs:\n$(cat "${LOG_FILE}")"
}

expect_body() {
  local expected="$1"
  local url="$2"
  shift 2
  local body
  body="$(curl --fail --silent --show-error "$@" "${url}" || true)"
  if [[ -z "${body}" && "${expected}" != "" ]]; then
    fail "request failed for ${url}"
  fi
  if [[ "${body}" != "${expected}" ]]; then
    fail "unexpected body for ${url}: ${body}"
  fi
}

expect_header() {
  local header="$1"
  local expected="$2"
  local url="$3"
  shift 3
  local headers
  headers="$(curl --silent --show-error --dump-header - --output /dev/null "$@" "${url}" 2>/dev/null || true)"
  if [[ -z "${headers}" ]]; then
    fail "request failed for ${url}"
  fi
  local value
  value="$(printf "%s" "${headers}" | awk -F': ' -v h="${header}" 'tolower($1)==tolower(h){print $2}' | tr -d '\r')"
  if [[ "${value}" != "${expected}" ]]; then
    fail "expected header ${header}: ${expected} for ${url}, got ${value}"
  fi
}

expect_no_header() {
  local header="$1"
  local url="$2"
  shift 2
  local headers
  headers="$(curl --silent --show-error --dump-header - --output /dev/null "$@" "${url}" 2>/dev/null || true)"
  if [[ -z "${headers}" ]]; then
    fail "request failed for ${url}"
  fi
  local value
  value="$(printf "%s" "${headers}" | awk -F': ' -v h="${header}" 'tolower($1)==tolower(h){print $2}' | tr -d '\r')"
  if [[ -n "${value}" ]]; then
    fail "expected header ${header} to be absent for ${url}, got ${value}"
  fi
}

expect_location() {
  local expected="$1"
  local url="$2"
  shift 2
  local headers
  headers="$(curl --silent --show-error --dump-header - --output /dev/null "$@" "${url}" 2>/dev/null || true)"
  if [[ -z "${headers}" ]]; then
    fail "request failed for ${url}"
  fi
  local value
  value="$(printf "%s" "${headers}" | awk -F': ' 'tolower($1)=="location"{print $2}' | tr -d '\r')"
  if [[ "${value}" != "${expected}" ]]; then
    fail "expected Location ${expected} for ${url}, got ${value}"
  fi
}

expect_listing_contains() {
  local expected="$1"
  local url="$2"
  shift 2
  local body
  body="$(curl --fail --silent --show-error "$@" "${url}" || true)"
  if [[ -z "${body}" ]]; then
    fail "request failed for ${url}"
  fi
  if [[ "${body}" != *"${expected}"* ]]; then
    fail "expected listing to contain ${expected}"
  fi
}

expect_listing_absent() {
  local forbidden="$1"
  local url="$2"
  shift 2
  local body
  body="$(curl --fail --silent --show-error "$@" "${url}" || true)"
  if [[ -z "${body}" ]]; then
    fail "request failed for ${url}"
  fi
  if [[ "${body}" == *"${forbidden}"* ]]; then
    fail "expected listing to omit ${forbidden}"
  fi
}

expect_curl_fail() {
  local url="$1"
  shift
  if curl --fail --silent --show-error "$@" "${url}" >/dev/null 2>&1; then
    fail "expected curl to fail for ${url}"
  fi
}

expect_file_content() {
  local expected="$1"
  local file_path="$2"
  if [[ ! -f "${file_path}" ]]; then
    fail "expected file to exist: ${file_path}"
  fi
  local actual
  actual="$(<"${file_path}")"
  if [[ "${actual}" != "${expected}" ]]; then
    fail "unexpected file content for ${file_path}: ${actual}"
  fi
}

expect_file_absent() {
  local file_path="$1"
  if [[ -e "${file_path}" ]]; then
    fail "expected file to be absent: ${file_path}"
  fi
}

wait_for_log_contains() {
  local expected="$1"
  local deadline=$((SECONDS + 5))
  while ((SECONDS < deadline)); do
    if grep -Fq "${expected}" "${LOG_FILE}"; then
      return 0
    fi
    sleep 0.1
  done
  fail "expected logs to contain: ${expected}. Logs:\n$(cat "${LOG_FILE}")"
}

cleanup_stale_pid() {
  if [[ ! -f "${PID_FILE}" ]]; then
    return
  fi
  local pid
  while read -r pid; do
    if [[ -z "${pid}" ]]; then
      continue
    fi
    if [[ ! -d "/proc/${pid}" ]]; then
      continue
    fi
    if tr '\0' '\n' <"/proc/${pid}/environ" 2>/dev/null | grep -q "SERV_INTEGRATION=1"; then
      kill "${pid}" >/dev/null 2>&1 || true
    fi
  done <"${PID_FILE}"
  rm -f "${PID_FILE}"
}

main() {
  require_cmd go
  require_cmd curl
  require_cmd openssl
  cleanup_stale_pid

  setup_site
  if [[ ! -x "${BIN_PATH}" ]]; then
    build_binary
  fi

  log "testing HTTP"
  local port
  start_server_retry -filter "*.log" -redirect "/r:https://example.com" -header "X-Test: ok"
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  expect_body "hello" "http://127.0.0.1:${port}/hello.txt"
  expect_header "X-Test" "ok" "http://127.0.0.1:${port}/hello.txt"
  expect_no_header "X-Test" "http://127.0.0.1:${port}/missing.txt"
  expect_listing_contains "hello.txt" "http://127.0.0.1:${port}/"
  expect_listing_absent "app.log" "http://127.0.0.1:${port}/"
  expect_listing_absent ".htaccess" "http://127.0.0.1:${port}/"
  expect_listing_absent ".hidden" "http://127.0.0.1:${port}/"
  expect_status "404" "http://127.0.0.1:${port}/.hidden"
  expect_status "401" "http://127.0.0.1:${port}/protected/secret.txt"
  expect_body "protected" "http://127.0.0.1:${port}/protected/secret.txt" -u "ht:pass"
  expect_status "401" "http://127.0.0.1:${port}/subauth/secret.txt"
  expect_body "subauth" "http://127.0.0.1:${port}/subauth/secret.txt" -u "sub:dir"
  expect_status "404" "http://127.0.0.1:${port}/protected/.htaccess"
  expect_status "404" "http://127.0.0.1:${port}/broken/file.txt"
  expect_status "404" "http://127.0.0.1:${port}/app.log"
  expect_status "403" "http://127.0.0.1:${port}/../hello.txt" --path-as-is
  expect_status "403" "http://127.0.0.1:${port}/%2e%2e/hello.txt" --path-as-is
  expect_status "403" "http://127.0.0.1:${port}/%252e%252e/hello.txt" --path-as-is
  local disabled_upload_source disabled_upload_name disabled_upload_target disabled_upload_response disabled_upload_code
  disabled_upload_source="${TMP_DIR}/upload-disabled-source.txt"
  disabled_upload_name="upload-without-flag.txt"
  disabled_upload_target="${SITE_DIR}/uploads/${disabled_upload_name}"
  disabled_upload_response="${TMP_DIR}/upload-disabled-response.txt"
  printf "upload must be disabled by default" >"${disabled_upload_source}"
  disabled_upload_code="$(curl --silent --show-error --output "${disabled_upload_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${disabled_upload_source}" "http://127.0.0.1:${port}/uploads/${disabled_upload_name}")"
  if [[ "${disabled_upload_code}" != "405" ]]; then
    fail "expected HTTP 405 when upload is disabled, got ${disabled_upload_code} body=$(cat "${disabled_upload_response}")"
  fi
  expect_file_absent "${disabled_upload_target}"
  wait_for_log_contains "\"POST /uploads/${disabled_upload_name} HTTP/1.1\" 405"
  expect_status "302" "http://127.0.0.1:${port}/r"
  expect_location "https://example.com" "http://127.0.0.1:${port}/r"
  if [[ "${SYMLINK_READY}" == "1" ]]; then
    expect_status "403" "http://127.0.0.1:${port}/escape.txt"
  fi
  if [[ "${HARDLINK_READY}" == "1" ]]; then
    expect_status "403" "http://127.0.0.1:${port}/hardlink.txt"
  fi
  stop_server

  log "testing one-time downloads"
  start_server_retry -otd otd
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  expect_body "download once" "http://127.0.0.1:${port}/otd/once.txt"
  expect_file_absent "${SITE_DIR}/otd/once.txt"
  expect_status "404" "http://127.0.0.1:${port}/otd/once.txt"
  stop_server

  log "testing one-time uploads"
  start_server_retry -otu drop -uploadoverwrite
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  expect_listing_absent "existing.txt" "http://127.0.0.1:${port}/drop/"
  local otu_source otu_target otu_name otu_hash otu_stored_name otu_response otu_code
  otu_source="${TMP_DIR}/one-time-upload-source.txt"
  otu_name="new-upload.txt"
  otu_response="${TMP_DIR}/one-time-upload-response.json"
  printf "one-time upload payload" >"${otu_source}"
  otu_hash="$(openssl dgst -sha256 -r "${otu_source}" 2>/dev/null | awk '{print $1}')"
  otu_stored_name="new-upload_${otu_hash}.txt"
  otu_target="${SITE_DIR}/drop/${otu_stored_name}"
  otu_code="$(curl --silent --show-error --output "${otu_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${otu_source}" "http://127.0.0.1:${port}/drop/${otu_name}")"
  if [[ "${otu_code}" != "201" ]]; then
    fail "expected HTTP 201 for one-time upload, got ${otu_code} body=$(cat "${otu_response}")"
  fi
  expect_file_content "one-time upload payload" "${otu_target}"
  expect_file_absent "${SITE_DIR}/drop/${otu_name}"
  expect_status "404" "http://127.0.0.1:${port}/drop/${otu_stored_name}"
  expect_listing_absent "${otu_stored_name}" "http://127.0.0.1:${port}/drop/"

  otu_code="$(curl --silent --show-error --output "${otu_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${otu_source}" "http://127.0.0.1:${port}/drop/existing.txt")"
  if [[ "${otu_code}" != "400" ]]; then
    fail "expected HTTP 400 for one-time upload duplicate sha256, got ${otu_code} body=$(cat "${otu_response}")"
  fi
  expect_file_content "existing upload" "${SITE_DIR}/drop/existing.txt"

  printf "replacement content" >"${otu_source}"
  otu_hash="$(openssl dgst -sha256 -r "${otu_source}" 2>/dev/null | awk '{print $1}')"
  otu_stored_name="existing_${otu_hash}.txt"
  otu_code="$(curl --silent --show-error --output "${otu_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${otu_source}" "http://127.0.0.1:${port}/drop/existing.txt")"
  if [[ "${otu_code}" != "201" ]]; then
    fail "expected HTTP 201 for one-time upload with reused filename and different sha256, got ${otu_code} body=$(cat "${otu_response}")"
  fi
  expect_file_content "existing upload" "${SITE_DIR}/drop/existing.txt"
  expect_file_content "replacement content" "${SITE_DIR}/drop/${otu_stored_name}"
  stop_server

  log "testing HTTP allowdotfiles + insecure"
  start_server_retry -allowdotfiles -insecure
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  expect_body "secret" "http://127.0.0.1:${port}/.hidden"
  if [[ "${SYMLINK_READY}" == "1" ]]; then
    expect_body "outside" "http://127.0.0.1:${port}/escape.txt"
  fi
  if [[ "${HARDLINK_READY}" == "1" ]]; then
    expect_body "hardlink" "http://127.0.0.1:${port}/hardlink.txt"
  fi
  stop_server

  log "testing basic auth"
  start_server_retry -username "user" -password "pass"
  port="${SERVER_PORT}"
  wait_for_status "401" "http://127.0.0.1:${port}/hello.txt"
  expect_body "hello" "http://127.0.0.1:${port}/hello.txt" -u "user:pass"
  stop_server

  log "testing upload + cleanup"
  start_server_retry -upload -uploadmaxmb 1
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  local upload_source upload_target upload_name upload_response upload_code upload_payload
  upload_source="${TMP_DIR}/upload-source.txt"
  upload_name="uploaded-integration.txt"
  upload_target="${SITE_DIR}/uploads/${upload_name}"
  upload_response="${TMP_DIR}/upload-response.json"
  upload_payload="uploaded integration payload"
  printf "%s" "${upload_payload}" >"${upload_source}"
  register_uploaded_test_file "${upload_target}"
  upload_code="$(curl --silent --show-error --output "${upload_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${upload_source}" "http://127.0.0.1:${port}/uploads/${upload_name}")"
  if [[ "${upload_code}" != "201" ]]; then
    fail "expected HTTP 201 for upload, got ${upload_code} body=$(cat "${upload_response}")"
  fi
  if ! grep -q '"uploaded":[[:space:]]*1' "${upload_response}"; then
    fail "expected uploaded count in response body: $(cat "${upload_response}")"
  fi
  if ! grep -q "\"name\":\"${upload_name}\"" "${upload_response}"; then
    fail "expected uploaded filename in response body: $(cat "${upload_response}")"
  fi
  expect_file_content "${upload_payload}" "${upload_target}"
  expect_body "${upload_payload}" "http://127.0.0.1:${port}/uploads/${upload_name}"
  cleanup_uploaded_test_files
  expect_file_absent "${upload_target}"
  expect_status "404" "http://127.0.0.1:${port}/uploads/${upload_name}"
  wait_for_log_contains "\"POST /uploads/${upload_name} HTTP/1.1\" 201"

  local blocked_source blocked_response blocked_upload_code
  blocked_source="${TMP_DIR}/upload-htaccess-source.txt"
  blocked_response="${TMP_DIR}/upload-htaccess-response.json"
  printf "username: bad\npassword: bad\n" >"${blocked_source}"
  blocked_upload_code="$(curl --silent --show-error --output "${blocked_response}" --write-out "%{http_code}" \
    -X POST --data-binary "@${blocked_source}" "http://127.0.0.1:${port}/uploads/.htaccess")"
  if [[ "${blocked_upload_code}" != "404" ]]; then
    fail "expected HTTP 404 for blocked upload target, got ${blocked_upload_code} body=$(cat "${blocked_response}")"
  fi
  wait_for_log_contains "\"POST /uploads/.htaccess HTTP/1.1\" 404"
  expect_file_absent "${SITE_DIR}/uploads/.htaccess"
  stop_server

  log "testing basic auth via env"
  export SERV_AUTH_USER="envuser"
  export SERV_AUTH_PASS="envpass"
  start_server_retry -username "env:SERV_AUTH_USER" -password "env:SERV_AUTH_PASS"
  port="${SERVER_PORT}"
  wait_for_status "401" "http://127.0.0.1:${port}/hello.txt"
  expect_body "hello" "http://127.0.0.1:${port}/hello.txt" -u "envuser:envpass"
  stop_server
  unset SERV_AUTH_USER
  unset SERV_AUTH_PASS

  log "testing basic auth via file"
  cat >"${TMP_DIR}/auth.json" <<'EOF'
{"username":"fileuser","password":"filepass"}
EOF
  start_server_retry -username "file:${TMP_DIR}/auth.json" -password "file:${TMP_DIR}/auth.json"
  port="${SERVER_PORT}"
  wait_for_status "401" "http://127.0.0.1:${port}/hello.txt"
  expect_body "hello" "http://127.0.0.1:${port}/hello.txt" -u "fileuser:filepass"
  stop_server

  log "testing allowed IPs"
  start_server_retry -allowedips "10.0.0.1"
  port="${SERVER_PORT}"
  wait_for_status "403" "http://127.0.0.1:${port}/hello.txt"
  stop_server
  start_server_retry -allowedips "127.0.0.1"
  port="${SERVER_PORT}"
  wait_for_url "http://127.0.0.1:${port}/hello.txt"
  expect_body "hello" "http://127.0.0.1:${port}/hello.txt"
  stop_server

  log "testing TLS"
  generate_certs
  start_server_retry -cert "${TLS_DIR}/server.pem" -key "${TLS_DIR}/server.key"
  port="${SERVER_PORT}"
  wait_for_url "https://127.0.0.1:${port}/hello.txt" --cacert "${TLS_DIR}/ca.pem"
  expect_body "hello" "https://127.0.0.1:${port}/hello.txt" --cacert "${TLS_DIR}/ca.pem"
  expect_listing_absent "server.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem"
  expect_listing_absent "server.key" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem"
  expect_listing_contains "ca.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem"
  expect_listing_contains "client.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem"
  expect_listing_contains "client.key" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem"
  expect_status "404" "https://127.0.0.1:${port}/tls/server.pem" --cacert "${TLS_DIR}/ca.pem"
  expect_status "404" "https://127.0.0.1:${port}/tls/server.key" --cacert "${TLS_DIR}/ca.pem"
  expect_status "200" "https://127.0.0.1:${port}/tls/ca.pem" --cacert "${TLS_DIR}/ca.pem"
  expect_status "200" "https://127.0.0.1:${port}/tls/client.pem" --cacert "${TLS_DIR}/ca.pem"
  expect_status "200" "https://127.0.0.1:${port}/tls/client.key" --cacert "${TLS_DIR}/ca.pem"
  stop_server

  log "testing mTLS"
  start_server_retry -cert "${TLS_DIR}/server.pem" -key "${TLS_DIR}/server.key" -cacert "${TLS_DIR}/ca.pem" -mtls
  port="${SERVER_PORT}"
  wait_for_url "https://127.0.0.1:${port}/hello.txt" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_curl_fail "https://127.0.0.1:${port}/hello.txt" --cacert "${TLS_DIR}/ca.pem"
  expect_body "hello" "https://127.0.0.1:${port}/hello.txt" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_listing_absent "server.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_listing_absent "server.key" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_listing_absent "ca.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_listing_contains "client.pem" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_listing_contains "client.key" "https://127.0.0.1:${port}/tls/" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_status "404" "https://127.0.0.1:${port}/tls/server.pem" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_status "404" "https://127.0.0.1:${port}/tls/server.key" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_status "404" "https://127.0.0.1:${port}/tls/ca.pem" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_status "200" "https://127.0.0.1:${port}/tls/client.pem" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  expect_status "200" "https://127.0.0.1:${port}/tls/client.key" --cacert "${TLS_DIR}/ca.pem" --cert "${TLS_DIR}/client.pem" --key "${TLS_DIR}/client.key"
  stop_server

  log "integration checks passed"
}

main "$@"
