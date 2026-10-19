#!/bin/bash
set -euo pipefail
cd /home/ubuntu/lbt174-validation/source
export PATH=/home/ubuntu/lbt174-validation/go/bin:$PATH
export CGO_ENABLED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2
export GOFLAGS='-mod=vendor -p=2'
unset GORACE || true
go version
gcc --version | head -n 1
go env GOOS GOARCH CGO_ENABLED GOTOOLCHAIN GOFLAGS
python3 - <<'PY'
import json,hashlib
v=json.load(open('tested-source.json'))
files={p:hashlib.sha256(open(p,'rb').read().replace(b'\r\n',b'\n')).hexdigest() for p in v['files']}
assert files==v['files']
print(json.dumps({'sourceFiles':len(files),'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(),'mismatches':0}))
PY
go vet -tags 'backend_router_harness integration' ./internal/server/app ./internal/server/connector
printf 'LBT174_LINUX_VET_PASS\n'
go build -tags backend_router_harness -o /tmp/labbit-c1-server ./cmd/labbit-server
printf 'LBT174_LINUX_BUILD_PASS\n'
go test -race -tags 'backend_router_harness integration' -run '^TestC1' -count=1 -json ./internal/server/app
printf 'LBT174_LINUX_RACE_PASS\n'
