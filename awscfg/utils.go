package awscfg

import (
	"net/url"

	"oss.nandlabs.io/golly/textutils"
)

// GetConfig resolves a Config by URL or fallback name.
// It tries the URL host first, then host+path, and finally the fallback name.
func GetConfig(u *url.URL, name string) (cfg *Config) {
	if u == nil {
		cfg = Manager.Get(name)
		return
	}

	key := ""
	if u.Host != "" {
		key = u.Host
	}

	cfg = Manager.Get(key)
	if cfg == nil {
		if u.Path != "" {
			key = key + textutils.ForwardSlashStr + u.Path
		}
		cfg = Manager.Get(key)
	}

	if cfg == nil {
		cfg = Manager.Get(name)
	}

	return
}
