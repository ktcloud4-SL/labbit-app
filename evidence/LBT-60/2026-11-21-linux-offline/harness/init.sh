#!/bin/sh
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root TMPDIR=/tmp LANG=C.UTF-8
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mkdir -p /dev/pts /tmp /run
mount -t devpts devpts /dev/pts
mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
ip link set lo up
echo '@@GUEST_READY@@'
uname -a
date -u '+%Y-%m-%dT%H:%M:%SZ'
cat /etc/alpine-release
ls -l /sys/class/net
bash --version | head -1
python3 --version
sha256sum --version | head -1
sha256sum /payload/manifest.json /payload/openstack.test /payload/installer.test /payload/test2json
echo '@@OPENSTACK_REUSE_ATTEMPT01@@'
echo '@@BEGIN_INSTALLER@@'
cd /app/internal/connector/installer
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/installer -t /payload/installer.test -test.v=test2json -test.timeout=180s
installer_result=$?
echo "@@END_INSTALLER:$installer_result@@"
echo '@@MANAGEMENT_ROUTE_REUSE_ATTEMPT01@@'
echo '@@BEGIN_WORKSPACEFILE@@'
cd /app/internal/connector/workspacefile
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile -t /payload/workspacefile.test -test.v=test2json -test.timeout=60s -test.run='^TestSymlinkRejectedAndExcludedFromTree$'
workspacefile_result=$?
echo "@@END_WORKSPACEFILE:$workspacefile_result@@"
echo '@@BEGIN_CREDENTIAL@@'
cd /app/tools/dev-auth-class-fixture
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/tools/dev-auth-class-fixture -t /payload/credential.test -test.v=test2json -test.timeout=60s -test.run='^TestInstallCredentialFile$'
credential_result=$?
echo "@@END_CREDENTIAL:$credential_result@@"
echo "@@GUEST_RESULTS:$installer_result:$workspacefile_result:$credential_result@@"
sync
poweroff -f
