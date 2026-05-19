package feature

import (
	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/logx"
)

// OpenLog opens the configured log sink (Config.LogFile or the default
// ~/.conduit/conduit.log) honoring Config.Verbose. It is opened lazily by
// a long-running feature so read-only commands never create ~/.conduit.
// On failure it falls back to the injected logger. The returned func
// closes the sink (safe no-op on the fallback path).
func (d Deps) OpenLog() (*logx.Logger, func()) {
	base := d.Log
	if base == nil {
		base = logx.New(nil)
	}
	path, verbose := "", false
	if d.Config != nil {
		path, verbose = d.Config.LogFile, d.Config.Verbose
	}
	if path == "" {
		p, err := logx.DefaultPath()
		if err != nil {
			base.Warnf("log: no se pudo resolver la ruta (sigo sin log en disco): %v", err)
			return base, func() {}
		}
		path = p
	}
	lg, closer, err := logx.OpenLeveled(path, verbose)
	if err != nil {
		base.Warnf("log: no se pudo abrir %s (sigo sin log en disco): %v", path, err)
		return base, func() {}
	}
	return lg, func() { _ = closer.Close() }
}

// WarnSecurity emits the D1/D2 advisories based on the resolved config:
// host-key verification disabled, and a plaintext password in the file.
func (d Deps) WarnSecurity(log *logx.Logger) {
	if d.Config == nil {
		return
	}
	if d.Config.Backend.SFTP.InsecureHostKey {
		log.Warnf("SEGURIDAD: verificación de host key DESHABILITADA " +
			"(--insecure-host-key); la conexión es vulnerable a MITM")
	}
	if d.Config.Backend.SFTP.Password != "" && !config.PasswordFromEnv() {
		log.Warnf("SEGURIDAD: el password está en texto plano en el archivo " +
			"de config; preferí la variable de entorno CONDUIT_PASSWORD")
	}
}
