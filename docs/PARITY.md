# PARITY.md — Especificación de paridad de `conduit`

Oracle normativo de la migración Python → Go. Define **exactamente** qué debe
hacer cada feature para ser equivalente al comportamiento de las herramientas
Python originales (`sftp_sync.py`, `sftp_download.py`). Las tablas de decisión
están expresadas sobre **operaciones abstractas `Storage`** (backend-neutrales),
no sobre SFTP.

> El oracle **ejecutable** vive en `testutil/fixtures` (paquete Go tipado). Esta
> doc y las fixtures deben coincidir; un test de consistencia lo verifica en CI.

---

## 0. Modelo y convenciones

- **mtime en enteros de segundos.** Todo `Storage.FileInfo.ModTimeUnix` y todo
  mtime local se **trunca a segundos** antes de comparar. Una diferencia
  sub-segundo es 0. Esto evita bucles de re-subida/re-descarga.
- **`delta = remote_mtime_sec − local_mtime_sec`** (enteros). Es el eje de las
  tablas.
- **Margen ±2 s, comparación estricta `<`** (heredado del Python: `+ 2 <`).
- "size distinto" se evalúa solo si el archivo existe en ambos lados.

---

## 1. Feature `watch` — espejo local → remoto (DESTRUCTIVO)

Port de `sftp_sync.py`. Sube/borra en el backend para que el remoto sea un
**espejo exacto** del local.

### 1.1 Ciclo de vida

1. `mkdir` carpeta local si falta.
2. `Storage.Connect`.
3. **Full resync** (§1.2).
4. Arrancar watcher (§1.4).
5. Loop: cada **30 s** `Storage.Ping`; si error → reconectar (`Close`+`Connect`).
6. Cualquier excepción de operación → reconectar y continuar.

### 1.2 Full resync — tabla de decisión por archivo

`L` = estado local, `R` = estado remoto. Acción sobre el archivo relativo:

| L | R | size igual | condición mtime | Acción `Storage` |
|---|---|------------|-----------------|------------------|
| presente | ausente | — | — | **`Put`** |
| presente | presente | no | — | **`Put`** |
| presente | presente | sí | `delta < -2` (local estrictamente más nuevo por > 2 s) | **`Put`** |
| presente | presente | sí | `delta ≥ -2` | **noop** |
| ausente | presente | — | — | **`Remove`** (archivo remoto que ya no existe local) |
| ausente | ausente | — | — | noop |

### 1.3 Poda de directorios

Tras procesar archivos: todo directorio remoto que **no** exista localmente se
elimina con **`RemoveDir`**, en orden **deepest-first** (los más profundos
primero) para que nunca se intente `RemoveDir` sobre un directorio no vacío.

- Caso obligatorio de test: árbol remoto `a/b/c` con archivos, ausente local →
  se borran archivos y luego `c`, `b`, `a` en ese orden. Nunca error por "no
  vacío".
- En backends tipo object store `RemoveDir` es no-op (ver §4): la poda es
  semánticamente nula pero el engine ejecuta la misma lógica.

### 1.4 Watcher (eventos en vivo, recursivo)

Solo archivos (eventos de directorio se ignoran salvo para gestionar watches).

| Evento | Operación(es) `Storage`, en orden |
|--------|-----------------------------------|
| `created` | esperar estable (§1.5) → `Put` |
| `modified` | esperar estable (§1.5) → `Put` |
| `deleted` | `Remove` |
| `moved` (src→dst) | `Remove` del src → esperar estable dst → `Put` del dst |

`fsnotify` **no es recursivo** (confirmado, issue #18). El watcher debe:
walk inicial + `Add` por cada subdirectorio + al recibir creación de subdir,
`Add` ese subdir **y re-walkearlo** para no perder archivos creados en la
ventana de carrera entre la creación y el `Add` (delta **D3**).

### 1.5 `wait_stable`

Antes de subir, sondear el tamaño del archivo hasta **15** veces cada **0.2 s**;
cuando dos lecturas consecutivas dan el mismo tamaño, está estable. Evita subir
archivos a medio escribir. El reloj debe ser **inyectable** (fake clock en
tests; nada de `time.Sleep` real).

---

## 2. Feature `download` — espejo remoto → local (ADITIVO / NO destructivo)

Port de `sftp_download.py`. Descarga lo que falta o cambió. **Nunca borra
archivos locales.**

### 2.1 Ciclo de vida

1. `mkdir` carpeta local si falta.
2. `Storage.Connect`.
3. **Full sync** (§2.2).
4. Loop: cada `max(5, poll_seconds || 15)` s → full sync. Reloj inyectable.
5. Error de operación → reconectar y continuar.

### 2.2 Full sync — tabla de decisión por archivo

| L | R | size igual | condición mtime | Acción `Storage` |
|---|---|------------|-----------------|------------------|
| ausente | presente | — | — | **`Get`** |
| presente | presente | no | — | **`Get`** |
| presente | presente | sí | `delta > 2` (remoto estrictamente más nuevo por > 2 s) | **`Get`** |
| presente | presente | sí | `delta ≤ 2` | **noop** |
| cualquiera | ausente | — | — | **noop** (nada que descargar) |
| presente | ausente | — | — | **noop** (NUNCA borra local) |

Tras `Get`, fijar el mtime local al `modTimeUnix` devuelto por `Storage.Get`
(truncado a segundos).

### 2.3 Filtro de archivos transitorios

El engine **ignora** entradas remotas cuyo nombre termina en `.part` o `.tmp`
(son archivos a medio subir del uploader). Filtro a nivel de engine, no del
driver.

---

## 3. Deltas vs. el Python (correcciones obligatorias)

| ID | Delta | Comportamiento Python | Comportamiento objetivo `conduit` | Step |
|----|-------|-----------------------|-----------------------------------|------|
| **D1** | Host key | `AutoAddPolicy()` — acepta cualquier host (riesgo MITM) | Driver SFTP verifica `known_hosts` por defecto vía `knownhosts.New` → `ssh.HostKeyCallback`. `*knownhosts.KeyError` con `Want` vacío = host desconocido (error claro), `Want` no vacío = mismatch (MITM, abortar). Flag `--insecure-host-key` reproduce lo viejo con WARNING logueado. | 7 |
| **D2** | Secretos | password en JSON plano | Soportar `CONDUIT_PASSWORD` por entorno; WARNING si el password está en archivo plano; migrar el JSON legacy `~/.sftpwatcher/config.json` → `~/.conduit/config.toml`. | 7 |
| **D3** | Watcher recursivo | `watchdog` recursivo nativo | `fsnotify` no recursivo: walk + `Add` por subdir + re-walk tras alta de watch para cubrir la ventana de carrera (no perder archivos). | 4 |

## 3.1 Limitaciones conocidas (heredadas, NO se corrigen)

- **Objeto remoto legítimo `*.part`/`*.tmp`:** `download` lo ignora siempre
  (§2.3). Si un usuario tiene un archivo real llamado `x.part`, nunca se
  descarga. Heredado del Python; documentado, no se cambia.
- **`.part` local huérfano:** si un `Get`/`Put` se interrumpe, queda un
  `<archivo>.part` local. No corrompe el siguiente sync (es un archivo local
  irrelevante para las decisiones de `download`, que solo mira el remoto). No
  hay limpieza automática.

---

## 4. Capacidades por backend

El engine **no cambia** entre backends; el driver absorbe las diferencias.

| Operación `Storage` | SFTP (v1) | Object store S3/MinIO (post-v1) |
|---------------------|-----------|---------------------------------|
| `Put` atómico | `<path>.part` + `PosixRename` (sobrescribe) | `PutObject` (atómico nativo) |
| `Get` atómico | `<local>.part` + replace; mtime vía remoto | `GetObject`; mtime ← `LastModified` |
| `List` recursivo | `ReadDir` recursivo / `Walk` | `ListObjectsV2` paginado |
| `EnsureDir` | `MkdirAll` | **no-op** (no hay directorios) |
| `RemoveDir` | `RemoveDirectory` (deepest-first lo ordena el engine) | **no-op** |
| `Remove` | `Remove` | `DeleteObject` |
| Host key (D1) | `knownhosts` (dentro del driver) | N/A (auth Signature V4) |
| `ModTimeUnix` | `FileInfo.ModTime()` truncado a s | `LastModified` truncado a s |

> **Anti-fuga:** si un caso de paridad solo se puede expresar con detalles de
> SFTP, el contrato `Storage` está mal — se corrige la interfaz, no se mete un
> `if isSFTP` en el engine.

---

## 5. Dependencias fijadas (resueltas vía proxy 2026-05-19)

| Módulo | Versión | Uso |
|--------|---------|-----|
| `github.com/spf13/cobra` | v1.10.2 | CLI / subcomandos (ya en Step 1) |
| `github.com/spf13/viper` | v1.21.0 | Config TOML+env+flags (Step 3/7) |
| `github.com/pkg/sftp` | v1.13.10 | Driver SFTP (`PosixRename`, `Chtimes`, `ReadDir`, `Walk`) |
| `golang.org/x/crypto` | v0.51.0 | `ssh` + `ssh/knownhosts` (D1) |
| `github.com/fsnotify/fsnotify` | v1.10.1 | Watcher (solo feature `watch`, Step 4) |
| `github.com/BurntSushi/toml` | v1.6.0 | Lectura/escritura TOML si se prefiere sobre viper-only |

Notas verificadas (search-first):
- `sftp.Client.PosixRename` es rename atómico que **sobrescribe** el destino →
  base del `Put` atómico SFTP. `Rename` falla si el destino existe (no usar).
- `sftp.Client.Chtimes(path, atime, mtime)` preserva mtime tras `Get`.
- `knownhosts.New(files...) (ssh.HostKeyCallback, error)`; distinguir host
  desconocido (`KeyError.Want` vacío) de mismatch MITM (`Want` no vacío).
- GoReleaser v2 `scoops:` hace **push directo** por defecto; cross-repo a
  `scoop-bucket` requiere `repository.token` = PAT (`SCOOP_BUCKET_TOKEN`).
- `fsnotify` confirmado **no recursivo**; mitigación D3 obligatoria.

---

## 6. Baseline del Python legacy (contraste opcional, recomendado)

Los `.py` legacy siguen presentes en `C:\...\SftpWatcher\` hasta el Step 8. Para
contrastar el comportamiento real (no solo el documentado):

1. Levantar el servidor SFTP embebido de tests (Step 3, `testutil/sftpserver`)
   o un `atmoz/sftp` local.
2. Configurar `sftp_sync.py` / `sftp_download.py` apuntando a él con un árbol
   de prueba derivado de `testutil/fixtures`.
3. Capturar las operaciones que ejecutan (log/trace) y compararlas con la tabla
   de §1.2/§2.2. Cualquier divergencia ⇒ la fixture o el doc están mal, no el
   Python.

Esto es una red de seguridad, no un gate. El gate es el test de consistencia
fixtures↔reglas + los tests de paridad de los engines (Steps 4/5).
