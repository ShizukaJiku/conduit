# conduit

Sincronizador de carpetas multi-PC, headless y **storage-agnóstico**, en un
único binario modular. Las funcionalidades son **plugins** (`watch`,
`download`, …) registrados en compilación; los backends de almacenamiento
(SFTP en v1; S3/MinIO después) viven detrás de una interfaz `Storage`.

> Migración nativa en Go de las herramientas Python `SftpWatcher` /
> `SftpDownloader`.

## Subcomandos

| Comando | Descripción |
|---------|-------------|
| `conduit watch` | Espeja una carpeta local **hacia** el backend (sube/borra; destructivo). |
| `conduit download` | Espeja el backend **hacia** una carpeta local (aditivo; nunca borra local). |
| `conduit features` | Lista las funcionalidades (plugins) disponibles. |
| `conduit version` | Versión, commit y fecha de build. |

`conduit --help` lista todo (los plugins provienen del registro, no están
hardcodeados). Ambos motores corren hasta `Ctrl+C`.

### `watch --screen` (captura de pantalla, Windows)

`conduit watch --screen` corre el espejo normal **y además** un capturador de
pantalla por hotkey en segundo plano. Flujo:

1. Pulsá el hotkey global (default **`Ctrl+Shift+S`**).
2. Hacé **clic izquierdo en 2 esquinas opuestas** del rectángulo a capturar
   (`Esc` cancela; si no clicás en ~30 s se cancela solo).
3. El PNG se guarda en `<local_folder>/<screen.dir>/` (default subcarpeta
   `screenshot/`) como `screenshot-AAAAMMDD-HHMMSS.mmm.png`.

Como esa subcarpeta está **dentro de la carpeta vigilada**, el propio `watch`
sube la captura al backend automáticamente. La captura no muestra overlay (es
invisible). Solo Windows; en otros SO el flag avisa y se desactiva sin afectar
el espejo. Un hotkey inválido o ya en uso también se desactiva sin abortar `watch`.

```toml
[screen]
hotkey = "ctrl+shift+s"   # modificadores ctrl/shift/alt/win + tecla A-Z, 0-9 o F1-F24
dir    = "screenshot"     # subcarpeta (relativa a local_folder) donde se guardan
```

| Flag | Env | Clave |
|------|-----|-------|
| `--screen` | — | (activa la captura; transitorio) |
| — | `CONDUIT_SCREEN_HOTKEY` | `screen.hotkey` |
| — | `CONDUIT_SCREEN_DIR` | `screen.dir` |

## Configuración

Precedencia: **flags > variables de entorno (`CONDUIT_*`) > archivo > defaults**.
Archivo por defecto: `~/.conduit/config.toml` (`%USERPROFILE%\.conduit\` en
Windows).

```toml
local_folder  = "C:/Users/yo/sync"
remote_folder = "/"          # raíz remota (relativa al backend)
poll_seconds  = 15           # download: intervalo (mínimo efectivo 5s)
log_file      = ""           # vacío → ~/.conduit/conduit.log
verbose       = false        # true → Info también a stderr

[backend]
type = "sftp"

[backend.sftp]
host = "files.example.com"
port = 22
user = "alice"
password = ""                 # ⚠ ver Seguridad — preferí CONDUIT_PASSWORD
known_hosts = ""              # vacío → ~/.ssh/known_hosts
insecure_host_key = false     # ⚠ true desactiva la verificación (MITM)
```

| Flag | Env | Clave |
|------|-----|-------|
| `--config` | — | ruta del archivo de config |
| `--backend` | `CONDUIT_BACKEND_TYPE` | `backend.type` |
| `--log-file` | `CONDUIT_LOG_FILE` | `log_file` |
| `--verbose` | `CONDUIT_VERBOSE` | `verbose` |
| `--insecure-host-key` | `CONDUIT_BACKEND_SFTP_INSECURE_HOST_KEY` | `backend.sftp.insecure_host_key` |
| — | `CONDUIT_PASSWORD` (alias) | `backend.sftp.password` |

## Seguridad

- **Host key (D1):** por defecto `conduit` **verifica** el host contra
  `known_hosts` (rechaza hosts desconocidos o cambiados → protección MITM).
  Las herramientas Python aceptaban cualquiera. `--insecure-host-key`
  reproduce ese comportamiento legacy y **loguea una advertencia**.
- **Secretos (D2):** poné el password en la variable de entorno
  `CONDUIT_PASSWORD` (o `CONDUIT_BACKEND_SFTP_PASSWORD`). Si está en el
  archivo de config en texto plano, `conduit` lo **avisa** al arrancar.

## Migración desde el Python legacy

En el primer arranque, si existe `~/.sftpwatcher/config.json` y todavía no
hay `~/.conduit/config.toml`, `conduit` lo **migra automáticamente** (mapea
`host/port/username/password/remote_folder/local_folder`) y escribe el
archivo con permisos `0600` (la JSON original quedaba legible por todos).
Se imprime un aviso con las rutas.

## Instalación (Scoop)

Disponible tras el primer release etiquetado (`v0.1.0`):

```powershell
scoop bucket add shizuka https://github.com/ShizukaJiku/scoop-bucket
scoop install conduit
conduit version
```

El manifest (`bucket/conduit.json`) lo genera y publica GoReleaser en cada
release; `scoop update conduit` trae la última versión.

## Release (mantenedor)

Disparado por un tag `v*` (`.github/workflows/release.yml` → GoReleaser):

```powershell
git tag v0.1.0
git push origin v0.1.0
```

Requiere el secret `SCOOP_BUCKET_TOKEN` en el repo (PAT fine-grained con
`Contents: Read and write` **solo** sobre `ShizukaJiku/scoop-bucket`).
Usá **expiración corta** y rotalo periódicamente; el workflow valida que
exista (fail-fast) *antes* de publicar el Release. Validar localmente sin
publicar:

```powershell
goreleaser check
goreleaser release --snapshot --clean
```

`conduit` **no usa `checkver`/`autoupdate`** por diseño: GoReleaser
regenera y pushea el manifest en cada release, así que `scoop update
conduit` siempre trae la última versión.

### Recuperación ante fallo parcial del release

GoReleaser publica el GitHub Release y *luego* pushea el manifest al
bucket. Si el job falla **después** de crear el Release (p.ej. el PAT
perdió permiso a mitad), el Release queda publicado pero el bucket
desactualizado. Para recuperar:

```powershell
gh release delete vX.Y.Z --repo ShizukaJiku/conduit --yes
git push origin :refs/tags/vX.Y.Z   # borrar el tag remoto
git tag -d vX.Y.Z
# corregir el secret/PAT, luego recrear el tag
git tag vX.Y.Z && git push origin vX.Y.Z
```

## Build desde fuente

```powershell
go build -o conduit.exe ./cmd/conduit
.\conduit.exe version
```

## Desarrollo

```powershell
go vet ./...
gofmt -l .
go test ./... -race -cover
go test -tags e2e ./testutil/e2e/... -race   # integración e2e
```

Nueva funcionalidad = nuevo paquete en `internal/feature/<name>/` que se
autoregistra vía `init()` + un blank-import en `cmd/conduit/main.go`. El
núcleo (`internal/cli`) no cambia.

## Licencia

[MIT](./LICENSE).
