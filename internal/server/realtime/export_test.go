package realtime

import "time"

// SetTestPingInterval must be called before the Relay serves connections.
func SetTestPingInterval(r *Relay, interval time.Duration) { r.pingInterval = interval }
