# conduit

Sincronizador de carpetas multi-PC, headless y **storage-agnóstico**, en un
único binario modular. Las funcionalidades son **plugins** (`watch`,
`download`, …) registrados en compilación; los backends de almacenamiento
(SFTP en v1; S3/MinIO después) viven detrás de una interfaz `Storage`.

> Migración nativa en Go de las herramientas Python `SftpWatcher` /
> `SftpDownloader`. Estado: **scaffolding (Step 1)** — los subcomandos son
> stubs; los motores se implementan en los siguientes steps.

## Subcomandos

| Comando | Descripción |
|---------|-------------|
| `conduit watch` | Espeja una carpeta local hacia el backend (destructivo). |
| `conduit download` | Espeja el backend hacia una carpeta local (aditivo). |
| `conduit version` | Versión, commit y fecha de build. |

`conduit --help` lista las funcionalidades disponibles (provienen del
registro de plugins, no están hardcodeadas).

## Instalación (Scoop)

> Disponible al completar el step de release.

```powershell
scoop bucket add shizuka https://github.com/ShizukaJiku/scoop-bucket
scoop install conduit
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
```

Nueva funcionalidad = nuevo paquete en `internal/feature/<name>/` que se
autoregistra vía `init()` + un blank-import en `cmd/conduit/main.go`. El
núcleo (`internal/cli`) no cambia.

## Licencia

[MIT](./LICENSE).
