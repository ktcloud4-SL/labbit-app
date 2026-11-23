#!/bin/sh
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root TMPDIR=/tmp LANG=C.UTF-8
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mkdir -p /tmp /run
mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
ip link set lo up
echo '@@GUEST_READY@@'
uname -a
ls -l /sys/class/net
sha256sum /payload/drcontrol.test /payload/test2json
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/drcontrol -t /payload/drcontrol.test -test.v=test2json -test.timeout=90s
result=$?
echo "@@LINKAGE_RESULT:$result@@"
sync
poweroff -f
