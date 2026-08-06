# EasyZFS — Specs de la fase P1 (v2.5.x)

Fecha: 6-Ago-2026 · Base: `main` (HEAD `8dc6508`). Roadmap: `docs/auditoria-2026-08-05-webzfs-zfdash.md` §7 (P1 reacotado).

**Estado: COMPLETADA y desplegada** (release v2.5.0, 6-Ago-2026): U1 y U3 implementadas en el PR #7 (merge `65620b4`), verificadas con go build/vet/test 12/12 + npm build + E2E demo. Este documento queda como especificación de referencia.

**Alcance de v2.5**: exactamente 2 features, las de medio esfuerzo de la auditoría:

| # | Feature | Backend | Frontend |
|---|---|---|---|
| U1 | SMART drill-down (atributos completos + selftests + error log) | `internal/collectors/smart.go`, `internal/httpapi/disks.go` | `web/src/views/Disks.tsx` |
| U3 | Tabla de propiedades del dataset + edición + inherit | `internal/actions/actions.go`, `internal/httpapi/datasets.go` | `web/src/components/Modals.tsx` |

El resto de P1 (S5 email/SMTP, U2 sparklines, U4 tooltips) pasa a P2 (decisión 6-Ago-2026).

**Regla de datos**: todo ejemplo en este documento usa pools/discos ficticios (`tank`, `ssd`, `sda`, `TEST0001`). NUNCA datos reales (serials, IPs, hostnames) en código, tests, fixtures ni capturas — repo público (ver `docs/auditoria-2026-08-05-webzfs-zfdash.md` y regla de datos en `easyzfs.md`).

**Criterio de "OK" por feature**: backend con `go build && go vet && go test` verde (nuevos tests incluidos), front `npm run build` (tsc + vite) limpio, mock actualizado, y verificación Playwright de la UI contra el binario demo (`DEMO=1`).

---

## U1 — SMART drill-down

### Contexto actual (verificado en `internal/collectors/smart.go`)

- El colector `smart` ya ejecuta `smartctl -j -a /dev/<dev>` por disco **cada 10 min** (timeout 60 s) y parsea el JSON en `parseSmartJSON` (struct `smartJSON`).
- Del JSON completo **solo se conserva un subconjunto**: estado ok/warn/crit, temperatura, horas, y los 4 contadores ATA (`Reallocated_Sector_Ct`, `Current_Pending_Sector`, `Offline_Uncorrectable`, `UDMA_CRC_Error_Count`) + `critical_warning` NVMe.
- El resto del JSON (tabla de atributos completa, flags, umbrales, self-test log, error log) **se descarta**.
- El front (`web/src/views/Disks.tsx`) muestra por fila: badge de estado, burbuja con contadores, y botones de test. La fila **no es clickeable** y no hay drill-down.

### Objetivo

Ver en la UI la **tabla de atributos SMART completa** (nombre, id, valor, worst, umbral, raw, estado `when_failed`), el **historial de self-tests** (tipo, resultado, % completado, horas de vida) y el **log de errores** de cada disco. Acceso: clic en la fila del disco (patrón clickable ya usado en `Datasets.tsx`).

### Principio rector (regla del proyecto)

> **NUNCA CLI desde HTTP.** Los handlers leen caché; los collectors son los únicos que ejecutan `smartctl`.

Por tanto NO se lanza `smartctl` on-demand en el GET. Los datos de detalle se capturan **en la pasada de 10 min que ya existe** (el JSON ya llega completo; solo hay que parsear y retener más campos). Consecuencia: los datos pueden tener hasta 10 min de antigüedad, lo que es aceptable para atributos SMART (son contadores lentos) y **se documenta en el contrato API**.

### Datos nuevos a retener (por disco)

Ampliar el struct `smartJSON` (o añadir uno `smartDetailJSON`) y un nuevo campo en `model.Disk` que guarde el detalle parseado.

```
type DiskSmartDetail struct {
    Protocol string         // "ata" | "nvme" | ""
    Attributes []SmartAttr  // tabla completa (ATA y NVMe unificada)
    Selftests  []SmartSelftest
    ErrorLog   SmartErrorLog
}
```

- **SmartAttr** (de `ata_smart_attributes.table[]` y, para NVMe, de `nvme_smart_health_information_log`):
  - `id` (int), `name`, `value`, `worst`, `thresh`, `raw` (string legible), `flags` (lista de strings), `when_failed` (`"-"` = ok, `"Past"`/`"In the past"` = superado umbral).
  - NVMe no tiene atributos de umbral en el mismo formato; se exponen los campos del health log (`available_spare`, `available_spare_threshold`, `percentage_used`, `temperature`, `critical_warning`, `media_errors`, `data_units_*`…) como filas `name/value` sin `worst`/`thresh`.
- **SmartSelftest** (de `ata_smart_selftest_log.standard.table[]`; NVMe: `nvme_self_test_log_1` — ver nota de verificación):
  - `type` (string), `status` (string), `lifetime_hours`, `segment`, `percent` (completado), `error` (opcional, si el status es de error).
- **SmartErrorLog**:
  - ATA: `count` (de `ata_error_log.summary.count`) + `entries` (últimas ~5, cada una con `timestamp`, `lifetime_hours`, `error_type`, `sector`/`lba` si aplica). El log de errores SATA puede ser verboso: limitar a las **últimas 5 entradas** + contador total.
  - NVMe: nota de verificación (nombre de sección a confirmar en runtime).

> **Nota de incertidumbre (fuente: smartctl 7.x `-j -a`)**. Los nombres de sección ATA (`ata_smart_attributes`, `ata_smart_selftest_log.standard`, `ata_error_log.summary`) son estables en smartctl 7.x. Los NVMe (`nvme_smart_health_information_log` — ya usado en el código — y el log de self-test/errores NVMe) conviene **confirmarlos con un `smartctl -j -a` real sobre un NVMe antes de cerrar el parseo**. Si difieren, el parseo es tolerante: ausencia de sección = lista vacía, no error.

### Endpoints nuevos (`internal/httpapi/disks.go`)

- `GET /api/disks/{dev}/smart` → 200 `{dev, model, serial, protocol, attributes:[SmartAttr], smart, smart_detail, hours}`
  - Datos de la caché del colector. 404 `not_found` si el disco no está en el inventario. Para discos `unknown` (sin smartctl: eMMC, USB sin SAT) → 200 con `attributes:[]` y `smart:"unknown"` (mismo criterio que `GET /api/disks`, no es un error).
- `GET /api/disks/{dev}/smart-log` → 200 `{dev, selftests:[SmartSelftest], error_log:{count, entries}}`
  - Idem caché. 404 `not_found` si el disco no existe. Listas vacías si el disco no expone logs.

Ambos GET **sin mutación y accesibles a cualquier rol** (lectura, como `GET /api/disks`). No requieren confirm.

### Persistencia del detalle en el colector

- `fillSmart` / `parseSmartJSON` guardan el detalle parseado en el `model.Disk` (nuevo campo `SmartDetail`→ renombrar para no chocar con `SmartDetail` string; usar `SmartFull *DiskSmartDetail`).
- Se publica junto con `disk.smart` por SSE tal cual hoy (el SSE no cambia; el detalle se pide bajo demanda).
- En `MOCK=1`, el mock devuelve atributos/selftests/error log ficticios coherentes con el estado del disco (ver mock.ts).

### Front (`web/src/views/Disks.tsx`)

- Fila de disco **clickeable** (patrón `Datasets.tsx`): `className="clickable"` + `onClick` abre un modal de detalle. Solo en escritorio/móvil sin afectar a los botones de acción (stopPropagation en los botones, como ya se hace en Datasets).
- **Modal de detalle** (componente nuevo `DiskDetailModal` en `web/src/components/Modals.tsx`, patrón `ModalBox`): cabecera con `dev` + `model` + badge de estado, y **3 pestañas/secciones**:
  1. **Atributos** — tabla (`table.data`) con columnas: nombre, valor (raw), value, worst, thresh, estado (`when_failed` ≠ "-" → badge `warn` "superado umbral"). Fila resaltada si `when_failed` indica fallo pasado.
  2. **Self-tests** — tabla: tipo, estado, % completado, horas de vida. Último primero.
  3. **Log de errores** — tabla: horas de vida, timestamp, tipo/error; o mensaje "sin errores registrados".
- Lazy load: los datos del modal se piden al abrir (`getDiskSmart(dev)` / `getDiskSmartLog(dev)`) con `Spinner`; error de red → `errorMessage`.
- i18n: claves nuevas `dsm_*` (ES/EN) en los locales (ver §i18n).

### Provider / mock

- `web/src/data/provider.ts`: `getDiskSmart(dev): Promise<DiskSmart>` y `getDiskSmartLog(dev): Promise<DiskSmartLog>`.
- `web/src/data/http.ts`: `get<DiskSmart>(\`/disks/${enc(dev)}/smart\`)` y `get<DiskSmartLog>(\`/disks/${enc(dev)}/smart-log\`)`.
- `web/src/data/mock.ts`: devuelve fixtures coherentes (disco `ok` → atributos con `when_failed:"-"`, 2 selftests "Completed without error"; disco `warn` → atributo realloc alto; disco `unknown` → listas vacías).
- `web/src/data/types.ts`: interfaces `SmartAttr`, `SmartSelftest`, `SmartErrorLog`, `DiskSmart`, `DiskSmartLog`.

### Tests sugeridos

- Go (nuevo `internal/collectors/smart_test.go` o ampliar):
  - Parseo de un fixture `smartctl -j -a` ATA **completo** → atributos con id/nombre/worst/thresh/raw/when_failed correctos; selftest log parseado; error log count+entries.
  - Fixture NVMe → health log mapeado a atributos; `attributes` no vacío.
  - Disco sin SMART (JSON de error "Unable to detect device type") → `SmartFull == nil`, listas vacías, sin fallo de pasada.
  - Selftest log vacío → `selftests: []`.
  - `smartctl` devuelve exit != 0 con JSON válido (bitfield de avisos) → se sigue parseando (ya cubierto por `RunTolerant`, añadir caso de detalle).
- Front: verificación manual/Playwright (login demo → vista Discos → clic fila → modal con atributos en ES y EN, tema claro/oscuro, responsive 390px sin overflow).

### Contrato API (añadir en `docs/api-contract.md` § Discos)

```
- GET /api/disks/{dev}/smart → 200 {dev, model, serial, protocol, smart, smart_detail, hours, attributes:[{id, name, value, worst, thresh, raw, flags[], when_failed}]}
  Caché del colector smart (hasta 10 min de antigüedad; nunca smartctl bajo demanda). Discos sin SMART → attributes:[].
- GET /api/disks/{dev}/smart-log → 200 {dev, selftests:[{type, status, lifetime_hours, percent}], error_log:{count, entries:[...]}}
```

---

## U3 — Tabla de propiedades del dataset + edición + inherit

### Contexto actual (verificado)

- `GET /api/datasets` devuelve un **subconjunto fijo** (`name, type, compression, used_bytes, avail_bytes, quota_bytes, mountpoint, encryption, keystatus`) desde `zfs list -Hp -o ...` (`internal/collectors/zpool.go:listDatasets`).
- `PATCH /api/datasets/{name}` solo acepta `quota_bytes` y `compression` (`DatasetPatch`).
- El modal de edición (`EditDatasetModal`) edita exactamente esos 2 campos.
- `DatasetCreate` valida compression con whitelist `lz4|zstd|off` y audita (`dataset.create`).
- Existen whitelists de nombres (`reDataset`, `rePool`…) en `internal/actions/actions.go`.

### Objetivo

Una **vista de propiedades** por dataset: tabla completa de propiedades (`zfs get -o name,property,value,source all <ds>`), con cada fila marcando origen (`local` / `default` / `inherited` / `received`) y acciones **Editar** (para las editables) e **Inherit** (para revertir a herencia). Whitelist estricta de propiedades y valores, mismo patrón que el resto del código.

### Datos

- `DatasetProp = { name, value, source }` donde `source ∈ {local, default, inherited, received, temporary, '-'}`.
- El valor se devuelve tal cual de `zfs get` (legible: `128K`, `lz4`, `on/off`), no bytes. La validación de valores al editar la hace el backend.

### Endpoints nuevos (`internal/httpapi/datasets.go`)

1. `GET /api/datasets/{name}/properties` → 200 `{name, properties:[DatasetProp]}` (admin y viewer lo ven; lectura).
   - Ejecuta `zfs get -H -o name,property,value,source all <ds>` vía el Service (collector de una pasada; ver "Principio" abajo).
   - **Filtro de exposición**: se devuelven todas las propiedades (nativas + user properties), porque `zfs get all` las lista y son informativas. La UI agrupa: propiedades nativas editables → nativas read-only → user properties (prefijo `user:`/`org.openzfs:`).
   - `findDataset` para validar existencia → 404 `not_found`.

2. `PATCH /api/datasets/{name}/properties` `{property, value}` (admin) → 204.
   - Valida `property` contra la **whitelist de editables** (tabla abajo) y `value` contra el tipo de esa propiedad. Errores: 400 `invalid_property` / 400 `invalid_value` (con mensaje legible del tipo esperado).
   - Ejecuta `zfs set <property>=<value> <ds>` y audita `dataset.setprop` (con `property` y `value`, nunca con secretos — las propiedades aquí no los tienen).
   - `volume` vs `fs`: algunas propiedades solo aplican a uno de los dos (ej. `volsize`/`volblocksize` solo volumen; `mountpoint` solo filesystem). Validar y rechazar 400 `invalid_value` si no aplica al tipo.

3. `POST /api/datasets/{name}/properties/{prop}/inherit` (admin) → 204.
   - Solo para propiedades de la whitelist con `source == "local"` (inherit sobre inherited/default es no-op; sobre received requiere `-S` que NO se expone).
   - Ejecuta `zfs inherit <prop> <ds>`, audita `dataset.inherit`.
   - 400 `invalid_property` si no es heredable/editables, 409 `not_local` si `source != local` (comprobado contra la última lectura de `zfs get`; si está obsoleta, zfs hará el no-op y es inocuo).

### Whitelist de propiedades editables (backend)

Tabla `propValidators`: propiedad → tipo → validación. **Regla**: solo propiedades seguras y útiles; las peligrosas (dedup, encryption, keyformat…) quedan **fuera** (cambiar dedup en caliente es delicado y encryption no se puede setear post-create). NUNCA se interpola el nombre/valor en el argv sin pasar la validación (patrón whitelist del proyecto).

| Propiedad | Tipo | Validación |
|---|---|---|
| `compression` | enum | `lz4\|zstd\|zlib\|gzip-1..9\|lzjb\|off` |
| `recordsize` | size | potencia de 2 entre `4K` y `16M` (formato `64K`, `128K`, `1M`…) |
| `atime` | bool | `on\|off` |
| `relatime` | bool | `on\|off` |
| `sync` | enum | `standard\|always\|disabled` |
| `checksum` | enum | `on\|off\|fletcher2\|fletcher4\|sha256` |
| `copies` | enum | `1\|2\|3` |
| `xattr` | enum | `on\|off\|sa` |
| `acltype` | enum | `off\|posix\|nfsv4` |
| `aclinherit` | enum | `discard\|noallow\|restricted\|passthrough\|passthrough-x` |
| `primarycache` | enum | `all\|none\|metadata` |
| `secondarycache` | enum | `all\|none\|metadata` |
| `logbias` | enum | `latency\|throughput` |
| `canmount` | enum | `on\|off\|noauto` |
| `mountpoint` | string | path absoluto `^\/[a-zA-Z0-9_\/.\-]+$` o `none\|legacy` |
| `exec` | bool | `on\|off` |
| `setuid` | bool | `on\|off` |
| `devices` | bool | `on\|off` |
| `readonly` | bool | `on\|off` |
| `snapdir` | enum | `hidden\|visible` |
| `quota` | size | `none` o tamaño (`1T`, `500G`…) — solo fs |
| `reservation` | size | `none` o tamaño — solo fs |
| `volsize` | size | tamaño — solo volume |
| `volblocksize` | size | potencia de 2 `512`..`128K` — solo volume |

- `bool`: regex `^(on|off)$`. `enum`: pertenencia exacta. `size`: `none` (cuando aplica) o regex `^[0-9]+[KMGTP]?[i]?[B]?$` (deja que zfs normalice; rechazar valores no numéricos).
- Las propiedades **read-only** (encryption, keystatus, type, used, available, referenced, compressratio, usedby*, written, objsetid, guid, keyformat…) **no están en la tabla** → `PATCH` las rechaza con `invalid_property`; `GET` las muestra con la UI sin botón Editar.
- **User properties**: se muestran en `GET` (agrupadas aparte) pero NO son editables vía esta tabla (fuera de alcance v2.5; queda para P2 con create/delete de user props).

### Principio del GET properties

El GET ejecuta `zfs get` (lectura, barato, ~1 comando por dataset). A diferencia de los datos de las vistas (caché de collectors), aquí el patrón es **lectura bajo demanda con caché TTL corta** (30 s por dataset, en memoria) porque: (a) `zfs get all` por dataset en el colector de 30 s sería N comandos por tick para datasets que nadie está mirando; (b) es una vista de inspección. Se documenta como excepción puntual a "NUNCA CLI desde HTTP" y se implementa con el TTL y single-flight si hay concurrencia. Alternativa conservadora (si el usuario prefiere cero excepción): colector que refresque propiedades SOLO de los datasets que tienen el modal abierto — descartada por complejidad; la excepción TTL 30 s es la que se adopta.

### Front (`web/src/components/Modals.tsx`)

- Sustituir `EditDatasetModal` por `DatasetPropsModal` (mismo disparador: clic en fila de `Datasets.tsx`). Contenido:
  - Cabecera: `ds.name` (mono) + badge tipo + badge cifrado/estado.
  - **Tabla de propiedades**: nombre (mono), valor actual (mono), badge de origen (`local`/`default`/`inherited`), y 2 acciones por fila cuando la propiedad es editable: **Editar** (botón que despliega un input/select inline según tipo) e **Inherit** (solo visible si `source == "local"`; confirmación de un clic con deshacer visual, no es destructiva).
  - Secciones: "Propiedades" (nativas) → "Solo lectura" (read-only sin botones) → "User properties" (solo lectura, agrupadas, prefijo `user:`).
  - Carga lazy al abrir el modal (`getDatasetProps(name)`), `Spinner`, error → `errorMessage`.
  - Edición inline: enum → `<select>`; bool → checkbox/Seg; size/string → `<input>` con placeholder del valor actual. Validación básica en cliente (regex del tipo) + error del backend mostrado tal cual.
- `web/src/data/provider.ts` / `http.ts` / `mock.ts` / `types.ts`: `getDatasetProps(name)`, `setDatasetProp(name, property, value)`, `inheritDatasetProp(name, property)`; interfaces `DatasetProp`, `DatasetPropsResp`. Mock: tabla ficticia de props por dataset.
- `Datasets.tsx`: la fila ya es `clickable` (abre `editds`). Cambiar el tipo de modal a `propsds`. Los botones de fila mantienen `stopPropagation`.
- i18n: claves `prp_*` (ES/EN): títulos, orígenes, "solo lectura", "user properties", errores de validación.

### Tests sugeridos

- Go (`internal/actions/actions_test.go` o nuevo `props_test.go`):
  - `zfs get` fixture → parsing correcto a `DatasetProp` con source.
  - Set de valor válido → argv exacto `zfs set <prop>=<val> <ds>` + audit `dataset.setprop`.
  - Set de valor inválido → 400 `invalid_value` (no llega a zfs).
  - Set de propiedad fuera de whitelist → 400 `invalid_property` (no llega a zfs).
  - Propiedad no aplicable al tipo (volsize en fs) → 400.
  - Inherit con source local → `zfs inherit`; inherit con source no-local → 409; inherit de propiedad no-whitelist → 400.
  - Names con `/` URL-encoded (`tank%2Fdocs`) ya cubiertos por el mux (confirmar en test de handler).
- Front: Playwright (login demo → Datasets → clic fila → modal props ES/EN, editar recordsize → 204 → valor cambia tras refresh; inherit en prop local; responsive 390px).

### Contrato API (añadir en `docs/api-contract.md` § Datasets)

```
- GET /api/datasets/{name}/properties → 200 {name, properties:[{name, value, source}]}
  Lectura bajo demanda (TTL 30 s, excepción puntual a la caché de colectores). 404 not_found.
- PATCH /api/datasets/{name}/properties {property, value} (admin) → 204
  400 invalid_property / 400 invalid_value (whitelist estricta de propiedades y valores).
- POST /api/datasets/{name}/properties/{prop}/inherit (admin) → 204
  400 invalid_property · 409 not_local (source != local).
```

---

## i18n (ambas features)

Claves nuevas en los locales ES y EN (`web/src/i18n/*`). Prefijos: `dsm_*` (SMART detail modal) y `prp_*` (props). Verificar paridad ES↔EN (regla de la casa). Sin cambio en el mecanismo (idioma por usuario en BD, `users.language`).

## Orden de implementación

1. **U3 propiedades** (backend primero: whitelist + validators → endpoints → tests Go → mock → front modal → i18n → build) — es la más autónoma y su whitelist es reutilizable.
2. **U1 SMART drill-down** (ampliar parseo/struct → endpoints → tests Go → mock → front modal → i18n → build).
3. Verificación final: `go build && go vet && go test`, `npm run build`, Playwright en modo demo, y **actualizar `docs/api-contract.md`** con los endpoints nuevos (sección correspondiente de cada feature).
4. Release v2.5.0 + despliegue en citadel-01/02 + **demo en vivo** (`demo.easyzfs.cloudless.club`) con el mismo binario.

## Criterio de "hecho"

Cada feature cerrada = código + tests verdes + mock + i18n + contrato API actualizado + verificado en modo demo. No se considera "hecho" por build solo: requiere los tests Go nuevos pasando y la UI verificada en demo (login demo).
