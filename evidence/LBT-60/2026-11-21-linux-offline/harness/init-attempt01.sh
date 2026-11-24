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
echo '@@BEGIN_OPENSTACK@@'
cd /app/internal/connector/provider/openstack
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack -t /payload/openstack.test -test.v=test2json -test.timeout=180s
os_result=$?
echo "@@END_OPENSTACK:$os_result@@"
echo '@@BEGIN_INSTALLER@@'
cd /app/internal/connector/installer
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/installer -t /payload/installer.test -test.v=test2json -test.timeout=180s
installer_result=$?
echo "@@END_INSTALLER:$installer_result@@"
cd /app
echo '@@BEGIN_MANAGEMENT_ROUTE@@'
python3 -m unittest discover -s internal/connector/provider/openstack -p ktcloud_management_route_guest_test.py -v
py_result=$?
echo "@@END_MANAGEMENT_ROUTE:$py_result@@"
echo '@@BEGIN_MANAGEMENT_ROUTE_OPTIMIZED@@'
python3 -O -m unittest discover -s internal/connector/provider/openstack -p ktcloud_management_route_guest_test.py -v
py_opt_result=$?
echo "@@END_MANAGEMENT_ROUTE_OPTIMIZED:$py_opt_result@@"
echo "@@GUEST_RESULTS:$os_result:$installer_result:$py_result:$py_opt_result@@"
sync
poweroff -f
