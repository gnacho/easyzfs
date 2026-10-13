# DESIGN.md — EasyZFS

## Discovery

- **Artefacto**: dashboard/herramienta de administración (pro tool) para ZFS, PWA
  self-hosted que vive en el propio NAS. Densidad media-alta, datos primero.
- **Audiencia**: homelab/admin técnico (repo OSS público). Posicionamiento: técnico,
  sobrio, fiable — una herramienta de consola con buena cara, no una landing.
- **Adjetivos de marca**: sobrio, preciso, técnico, honesto.
- **Esencia (3 palabras)**: instrumento de consola.

## Dirección estética

Evolución de la base existente (verde sobre crema / verde menta sobre casi-negro),
sin rediseño de identidad. **Classic es la cara canónica y la familia por defecto**
(README, capturas y soporte); las otras tres skins se conservan como variantes con
rol propio. El trabajo de esta ronda (1-Oct-2026) quita los tells que quedaban:
violeta reintroducido en Modern, halos/glows decorativos y micro-texto funcional por
debajo de 11.5px. La paleta de Classic se mantiene tal cual (crema original).

**Signature move**: los datos técnicos (pools, vdevs, series, rutas, comandos)
van SIEMPRE en JetBrains Mono — la app se lee como una consola bien tipografiada,
con Archivo para la voz de la interfaz.

## Tipografía

- **Display + body**: Archivo (400/500/600/700), self-hosted (@fontsource,
  funciona offline en LAN). Grotesca de interfaz, con carácter y legible a 13-15px.
- **Datos**: JetBrains Mono (400/500/600) para nombres de pool/disco/dataset,
  series, rutas, comandos cron, tamaños y temps en tablas.
- **Escala**: base 13.5-14.5px, h1 22px/700, h2 sección 15px/700, labels 11-12px
  uppercase tracking .05em. **Suelo de texto funcional 11.5px** (nav, stats de
  sidebar, badges, cabeceras de tabla): por debajo solo decoración, nunca datos
  (1-Oct-2026).

## Color

- Sistema existente se mantiene: neutros verdosos (bg crema `#f6f6f3` /
  dark `#0e1210`), semánticos ok/warn/err/info.
- **Acento: picker por familia (se conserva, decisión del usuario)** con
  opciones curadas. `emerald` en classic (defecto, `#2f7d5f` / dark `#5cb893`),
  `steel` en modern (defecto nuevo, `#3a6ea5` / dark `#7ba7d9`), cyan/amber en
  phosphor y brutalist. **Violeta eliminado del código y de los presets** (banda
  índigo/violeta = red ocean de UI generativa); la elección guardada por un
  usuario de Modern con violeta cae al nuevo defecto steel.
- Cada acento define par [accent, accent-soft] por tema (light/dark).
- **Nota (1-Oct-2026)**: se probó un retune AA de `--text3` y de los semánticos
  y finalmente se revirtió a la paleta original por decisión de producto. Queda
  como deuda conocida el contraste flojo de `--text3` (~3.3:1) y `warn` (~3.1:1)
  en Classic; si se retoma, la tabla propuesta está en el histórico de la rama.

## Tokens

| Token | Valor | Nota |
|---|---|---|
| `--font-sans` | 'Archivo', system-ui, sans-serif | voz de la UI |
| `--font-mono` | 'JetBrains Mono', ui-monospace, monospace | datos técnicos |
| `--radius` | 10px | tarjetas y paneles |
| `--radius-sm` | 7px | controles (botones, inputs, chips) |
| sombra | SOLO overlays (modal, popover alertas) | tarjetas: borde XOR sombra |
| `--shadow` | 0 8px 28px rgba(10,14,10,.18) | elevación de overlays |

**Regla borde-XOR-sombra**: un elemento en reposo lleva borde hairline O sombra
suave, nunca ambos. Las tarjetas pierden `box-shadow` (ya tienen borde).

**Regla anti-halo**: sin `box-shadow` de color a cero offset (glow/halo) como
decoración; la elevación lleva offset y blur neutros. Los LEDs de estado y el
botón primario de Modern ya no llevan glow (1-Oct-2026).

## Craft

- **Layout**: en pantallas de hasta ~2100px el contenido se limita a 1400px
  para conservar huecos laterales (1080p y 1440p pequeños); por encima, 1600px.
- **Tablas**: columnas numéricas (tamaño, temp, horas) alineadas a la derecha con
  `font-variant-numeric: tabular-nums`; nombres de dispositivo en mono.
- **Botones**: jerarquía por importancia (primary solo para la acción principal
  de cada vista/modal; ghost para el resto; danger solo texto).
- **Estados**: hover/disabled/focus-visible ya existentes; se mantiene el
  `:focus-visible` con outline de acento.
- **Movimiento**: fade de vista 180ms, modal 220ms, `prefers-reduced-motion`
  respetado (ya implementado). Sin animaciones en acciones de alta frecuencia.
- **Iconografía**: set inline propio (stroke coherente) — se mantiene.
- **Dark mode**: diseñado por tema (no invertido), acentos desaturados en dark
  (los pares de ACCENTS ya lo hacen).

## Slop-audit (1-Ago-2026)

| Tell | Estado |
|---|---|
| Fuente del sistema/Inter como principal | CORREGIDO → Space Grotesk (hoy Archivo, 1-Oct-2026) |
| Borde + sombra difusa apilados | CORREGIDO → tarjetas solo borde |
| Radius >16px en tarjetas | OK (14→12) |
| Violeta en picker de acento | CORREGIDO → steel |
| Datos técnicos sin mono | CORREGIDO → JetBrains Mono |
| Columnas numéricas centradas/no tabulares | CORREGIDO → derecha + tabular-nums |
| Cream bg por reflejo | ACEPTADO consciente (base aprobada, tema dark alternativo) |
| Violeta `#7c5cfc` reintroducido en Modern (default) | CORREGIDO 1-Oct-2026 → steel + default classic |
| Glow/halo de acento (botón, nav, foco, LEDs, halo radial) | CORREGIDO 1-Oct-2026 → sombra neutra + outline |
| Micro-texto funcional a 9.5-10.5px | CORREGIDO 1-Oct-2026 → suelo 11.5px |
| `--text3`/semánticos bajo 4.5:1 | REVERTIDO 1-Oct-2026 (decisión de producto): se mantiene la paleta original; contraste flojo conocido |

## Changelog

- 1-Oct-2026: F1 del refactor anti-slop: default classic; Modern sin violeta ni
  glow; fuente Archivo para la voz UI (JetBrains Mono se mantiene para datos);
  suelo tipográfico 11.5px; iconos de trazo en estados, chips de contorno y
  cabeceras neutras; Dashboard en rejilla compacta con gráfica de ocupación.
  La paleta de Classic se mantiene sin cambios (se probó un retune AA y se
  revirtió).
- 1-Ago-2026: primera pasada de la skill frontend-design-deslop (tokens, tipografía,
  acentos curados, tablas numéricas, borde-XOR-sombra).
