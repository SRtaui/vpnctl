// Package paths holds the filesystem locations used by vpnctl.
package paths

const (
	EtcDir        = "/etc/vpnctl"
	SettingsFile  = EtcDir + "/settings.json"
	SingBoxConfig = EtcDir + "/sing-box.json"

	LibDir     = "/var/lib/vpnctl"
	BinDir     = LibDir + "/bin"
	SingBoxBin = BinDir + "/sing-box"
	RuleSetDir = LibDir + "/rules"
	GeositeRU  = RuleSetDir + "/geosite-ru.srs"
	GeoipRU    = RuleSetDir + "/geoip-ru.srs"

	PidFile = "/run/vpnctl.pid"
	LogFile = "/var/log/vpnctl.log"

	ServiceName = "vpnctl"
)
