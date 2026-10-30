# Referencia de la API v1

[English](../docs/api.md) | **Español**

La API JSON conecta otros sistemas con tkt: permite consultar categorías, crear tickets,
completar formularios del solicitante, seguir el progreso y responder. La ruta base es
`/api/v1`. Solo root la habilita en **Configuración → API**; está apagada por defecto.
La creación de usuarios de servicio y claves se describe en
[Acceso a la API](administration.md#acceso-a-la-api).

## Autenticación y acceso

Cada solicitud debe incluir una sola cabecera:

```http
Authorization: Bearer tkt_<id>_<secret>
```

Se usa el token completo mostrado al crear la clave. La API autentica con tokens Bearer,
no con cookies del navegador; no usa sesiones del navegador ni tokens CSRF.
Las respuestas correctas usan `application/json` y `Cache-Control: no-store`.
Los cuerpos POST usan `Content-Type: application/json`. Los campos desconocidos, el JSON
mal formado y los valores JSON múltiples se rechazan con 400; un tipo de contenido no
admitido recibe 415.

- Con la API apagada, toda solicitud a `/api/*` devuelve 404, incluso con credenciales inválidas.
- Las credenciales ausentes, mal formadas, desconocidas, vencidas o revocadas, y las claves
  de usuarios inactivos, reciben la misma respuesta 401 con `WWW-Authenticate: Bearer`.
- La falta del alcance que pide el endpoint devuelve 403 `scope-missing`; una restricción
  de rol o identidad devuelve 403 `forbidden`. Los alcances no anulan las reglas de autorización.
- Un ticket fuera del acceso permitido devuelve el mismo 404 que un identificador inexistente
  o mal formado. La respuesta no revela si el ticket existe.
- Cada clave tiene un límite de **60 solicitudes por minuto**, con una reserva de 60 que
  se repone a una solicitud por segundo. Agotarla devuelve 429 con `Retry-After` en segundos.
  El límite es local al proceso y se reinicia al reiniciar el servidor; no se comparte entre procesos.
- `last_used_at` significa **presentación de credenciales válidas**, no «solicitud correcta».
  Se registra como máximo una vez por minuto, aunque después se rechace la ruta, el alcance,
  el cuerpo o la solicitud por exceso de frecuencia. Las credenciales rechazadas no lo actualizan.

### Alcances

El permiso efectivo es **rol ∩ alcances**: el endpoint comprueba el alcance de la clave y
la aplicación comprueba el rol, el acceso al ticket y los requisitos de identidad.

| Alcance | Permite |
| --- | --- |
| `catalog:read` | Listar categorías con flujos ejecutables y sus formularios del solicitante. |
| `tickets:read` | Listar, buscar y leer tickets dentro del acceso permitido. |
| `tickets:write` | Crear tickets, cambiar de estado, completar pasos y confirmar o rechazar la resolución, según las reglas de rol e identidad. |
| `comments:write` | Agregar comentarios públicos. |
| `comments:internal` | Agregar comentarios internos; solo usuarios de servicio agentes. |
| `tickets:all` | Solo usuarios de servicio agentes: amplía el acceso a toda la cola para lecturas y escrituras. Sigue siendo necesario el alcance del endpoint. |

Un usuario de servicio con rol Usuario lee sus propios tickets. Un usuario de servicio
Agente no tiene asignaciones ni pertenece a mesas, por lo que sin `tickets:all` su acceso
de lectura de tickets está vacío. Puede crear un ticket, pero no leerlo después.
Con `tickets:all` sigue siendo agente: no obtiene permisos de gestión de usuarios ni de
configuración, no evita las comprobaciones de identidad del solicitante o responsable,
y no puede tomar tickets ni ser responsable. Las restricciones de asignación del agente se mantienen.
La lectura del detalle por un agente con `tickets:read` incluye los comentarios internos;
escribirlos requiere `comments:internal`.

> [!WARNING]
> `tickets:all` expone toda la cola. Una clave filtrada con los alcances de escritura
> correspondientes puede cambiar cualquier ticket. Solo debe otorgarse a integraciones
> que necesiten ese acceso.

## Detalles de problemas

Los errores usan RFC 9457, `application/problem+json` y `Cache-Control: no-store`:

| Campo | Tipo | Significado |
| --- | --- | --- |
| `type` | cadena | URI estable del problema; permite distinguir los errores. |
| `title` | cadena | Título breve del problema. |
| `status` | entero | Estado HTTP. |
| `detail` | cadena, opcional | Explicación en inglés; se omite si está vacía. Su redacción puede cambiar. |
| `instance` | cadena, opcional | Ruta de la solicitud, incluida en estos endpoints. |
| `errors` | arreglo, opcional | Extensión de validación: objetos con las cadenas `field` y `detail`. Se incluye cuando el error de validación identifica un campo. |

Por ejemplo, `GET /api/v1/tickets?state=unknown` devuelve 422:

```json
{
  "type": "urn:tkt:problem:validation",
  "title": "Invalid request",
  "status": 422,
  "detail": "unknown ticket state",
  "instance": "/api/v1/tickets",
  "errors": [{"field": "state", "detail": "unknown ticket state"}]
}
```

Este es el vocabulario completo de problemas:

| Tipo | Estado | Significado |
| --- | --- | --- |
| `urn:tkt:problem:bad-request` | 400 | JSON mal formado, cursor inválido o entrada de transporte inválida. |
| `urn:tkt:problem:unauthorized` | 401 | Credenciales inválidas. |
| `urn:tkt:problem:forbidden` | 403 | Rol, identidad u operación no permitidos. |
| `urn:tkt:problem:scope-missing` | 403 | La clave no tiene el alcance que requiere el endpoint. |
| `urn:tkt:problem:not-found` | 404 | API apagada, ruta o recurso inexistente, o ticket fuera del acceso permitido. |
| `urn:tkt:problem:method-not-allowed` | 405 | La ruta no acepta ese método; la cabecera `Allow` indica los admitidos. |
| `urn:tkt:problem:conflict` | 409 | Conflicto de recursos, flujo que no puede avanzar o cambio concurrente de una espera. |
| `urn:tkt:problem:request-too-large` | 413 | La solicitud supera el límite de tamaño del transporte. |
| `urn:tkt:problem:unsupported-media-type` | 415 | El tipo de contenido del cuerpo no es `application/json`. |
| `urn:tkt:problem:validation` | 422 | Entrada inválida en un campo. |
| `urn:tkt:problem:rejected` | 422 | Una regla de dominio rechaza la operación, incluida una transición inválida o una posición de flujo desactualizada. |
| `urn:tkt:problem:rate-limited` | 429 | Se agotó la reserva de solicitudes de la clave. |
| `urn:tkt:problem:internal` | 500 | Fallo interno; no se exponen detalles de implementación. |

Todos los endpoints pueden devolver los errores de autenticación, frecuencia, rutas e internos
anteriores. Los POST comparten además los errores de JSON y de cuerpo. Cada sección destaca
los errores adicionales.

## GET /api/v1/me

**Alcance:** ninguno más allá de una clave válida. Sin cuerpo ni parámetros de consulta.
**Respuesta:** 200, con el entero `id`, las cadenas `name` y `role` del usuario de servicio,
y el arreglo de cadenas `scopes`.

```json
{"id":12,"name":"Monitoring","role":"user","scopes":["catalog:read","tickets:read","tickets:write","comments:write"]}
```

## GET /api/v1/categories

**Alcance:** `catalog:read`. Sin cuerpo ni parámetros de consulta. **Respuesta:** 200.
Solo lista categorías con un flujo publicado y ejecutable; publicarlo no basta.
La lista no está paginada (`next_cursor` es null).

```json
{
  "items": [{
    "id": 3, "name": "Network", "desk": "Support", "department": "IT",
    "requester_form": {
      "position": 1,
      "fields": [{"name":"location","label":"Location","type":"short_text","required":true,"options":[]}]
    }
  }],
  "next_cursor": null
}
```

`id` es entero; el nombre, la mesa y el departamento son cadenas. `requester_form` es null
si no existe; de lo contrario, contiene el **primer formulario del solicitante** del flujo
publicado. Su entero `position` empieza en uno. Cada campo tiene las cadenas `name`, `label`
y `type`, el booleano `required` y el arreglo de cadenas `options`. Los tipos son `short_text`,
`long_text`, `checkbox` y `single_select`. Falta de alcance: 403.

## GET /api/v1/tickets

**Alcance:** `tickets:read`. **Respuesta:** 200. Sin cuerpo.

| Parámetro de consulta | Tipo | Significado |
| --- | --- | --- |
| `q` | cadena, opcional | Busca por título o número de ticket, por ejemplo `TKT-42`; no por descripción ni comentarios. |
| `state` | cadena, repetible | `new`, `in_progress`, `resolved`, `closed` o `cancelled`. |
| `cursor` | cadena, opcional | Token opaco de la página siguiente recibido en la respuesta anterior; se envía uno solo. |

Cada página tiene como máximo **10** tickets. `next_cursor` es una cadena si hay otra página;
de lo contrario, es null. Se mantienen los filtros y se envía el token sin modificarlo:
no se debe decodificar ni construir un cursor. No hay parámetro de tamaño de página.

Para usuarios de servicio con **rol Usuario**, los valores de estado se validan, pero las
reglas de secciones del solicitante deciden qué estados se listan en lugar del filtro enviado:
este endpoint lista sus tickets `new` e `in_progress`, incluidos los que están en espera.
No expone un selector de sección. El acceso al detalle de un ticket propio no está limitado
a esos estados.

```json
{
  "items": [{
    "id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium",
    "category":{"id":3,"name":"Network"},"assignee":null,
    "requester":{"id":12,"name":"Monitoring"},
    "created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z"
  }],
  "next_cursor": null
}
```

Este es el **resumen del ticket**, que también devuelve la creación. `id` y `number` son
enteros; `title`, `state` y `priority` son cadenas. Las referencias contienen el entero `id`
y la cadena `name`; `assignee` y `requester` pueden ser null. Las fechas son cadenas RFC 3339
en UTC. Cursor inválido: 400; estado desconocido: 422 `validation`; falta de alcance: 403.

## GET /api/v1/tickets/{id}

**Alcance:** `tickets:read`. `id` es el identificador entero positivo del ticket, no su número
visible. Sin cuerpo ni parámetros de consulta. **Respuesta:** 200, un resumen del ticket más
la cadena `description`, los arreglos `comments` y `timeline`, y el objeto anulable `workflow`.

```json
{
  "id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium",
  "category":{"id":3,"name":"Network"},"assignee":null,
  "requester":{"id":12,"name":"Monitoring"},
  "created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z",
  "description":"The office network is unavailable.",
  "comments":[{"id":7,"author":{"id":12,"name":"Monitoring"},"visibility":"public","body":"Only the second floor is affected.","created_at":"2026-10-23T09:01:00Z"}],
  "timeline":[{"action":"created","field":null,"from":null,"to":null,"actor":{"id":12,"name":"Monitoring"},"at":"2026-10-23T09:00:00Z"}],
  "workflow":{"current_position":1,"pending_step":{"position":1,"kind":"form","fields":[{"name":"location","label":"Location","type":"short_text","required":true,"options":[]}]}}
}
```

Los comentarios tienen el entero `id`, el autor `{id, name}` (el `id` histórico puede ser null),
la cadena `visibility` (`public` o `internal`), la cadena `body` y la fecha UTC RFC 3339
`created_at`. Cada entrada de la cronología tiene la cadena `action`, las cadenas anulables
`field`, `from` y `to`, el objeto anulable `actor` (la misma identidad histórica) y la fecha
UTC RFC 3339 `at`. Los comentarios están separados de la cronología de auditoría; los eventos
automáticos pueden tener actor null. Esta proyección no incluye el identificador de la clave.

`workflow` es null si no hay ejecución de flujo. Si existe, `current_position` empieza en uno;
`pending_step` es null salvo que el usuario pueda actuar sobre el paso activo actual. Un paso
pendiente tiene el entero `position`, la cadena `kind` (`form` o `manual_task`) y `fields` con
la misma estructura que los campos de categoría. El acceso a toda la cola no convierte al
usuario en solicitante ni responsable. Falta de alcance: 403; identificador mal formado,
inexistente o fuera del acceso permitido: 404.

## POST /api/v1/tickets

**Alcance:** `tickets:write`. Crea el ticket mediante el flujo de la categoría. El usuario de
servicio siempre es el solicitante; no se admiten campos para elegir solicitante o responsable.

| Campo del cuerpo | Tipo | Significado |
| --- | --- | --- |
| `category_id` | entero | Categoría existente con un flujo publicado y ejecutable. |
| `title` | cadena | Obligatorio, no vacío; se recortan los espacios de los extremos. |
| `description` | cadena, opcional | Descripción; vacía por defecto. |
| `priority` | cadena | Obligatoria: `low`, `medium`, `high` o `critical`. |

```json
{"category_id":3,"title":"Network unavailable","description":"The office network is unavailable.","priority":"medium"}
```

**Respuesta:** 201 con `Location: /api/v1/tickets/42` y un resumen del ticket, por ejemplo:

```json
{"id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium","category":{"id":3,"name":"Network"},"assignee":null,"requester":{"id":12,"name":"Monitoring"},"created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z"}
```

El estado y el responsable dependen de los primeros pasos automáticos del flujo. La creación
no recibe respuestas de formularios: se consulta el detalle y se completa por separado el
formulario pendiente del solicitante. Un usuario de servicio Agente **sin `tickets:all`
puede crear el ticket, pero no leerlo después**; el 201 confirma la creación, no concede lectura.
Categoría inexistente: 404; título vacío o falta de flujo publicado: 422 `validation`;
prioridad inválida: 422 `rejected`; un flujo cuyo primer paso automático asigna por menor carga a
una mesa sin miembros: 409 `conflict`, sin crear nada.

`GET /api/v1/categories` lista solo las categorías que se pueden ejecutar ahora, pero la creación no
consulta esa lista. Una categoría cuyo primer paso de mesa es un reclamo sobre una mesa sin
miembros igual acepta el ticket, como el formulario del navegador, y el ticket espera hasta que la
mesa tenga a alguien que lo reclame. Conviene consultar primero las categorías para crear solo en
las que se pueden ejecutar.

## POST /api/v1/tickets/{id}/workflow/steps/{position}/complete

**Alcance:** `tickets:write`. `id` es un entero positivo; `position` en la ruta es el paso
actual del flujo y **empieza en uno**. Se usa `workflow.pending_step` del detalle, no una
posición supuesta.

| Campo del cuerpo | Tipo | Significado |
| --- | --- | --- |
| `answers` | arreglo, opcional | Objetos con el entero `position` y el arreglo de cadenas `values`. Las posiciones de respuesta son **índices de campos que empiezan en cero**, no nombres de campos ni posiciones de pasos. |
| `solution` | cadena o null, opcional | Solución exclusiva de tareas manuales; se recortan los espacios de los extremos, con un máximo de 2.000 bytes. Se rechaza una solución no vacía en otro tipo de paso. |

```json
{"answers":[{"position":0,"values":["Second floor"]}]}
```

Se rechazan posiciones duplicadas, desconocidas o ambiguas (más de un valor). Los campos de
texto reciben una cadena; una selección recibe un valor exacto de `options`. Para marcar una
casilla se envía `"on"` o `"true"`; omitirla o enviar un valor vacío la deja sin marcar. Una
casilla obligatoria debe estar marcada. El flujo valida los campos obligatorios y sus tipos.
Solo el solicitante registrado puede completar su
formulario; las acciones del responsable requieren ser el responsable registrado. Los usuarios
de servicio no pueden tomar tickets y `tickets:all` no evita estas comprobaciones de identidad.
**Respuesta:** 200, el detalle completo mostrado antes, con el avance del flujo y la cronología
actualizada; esta respuesta no requiere además `tickets:read`.
Ticket inexistente o fuera del acceso permitido: 404; actor incorrecto: 403; respuestas
inválidas: 422 `validation`; posición desactualizada, inválida o inactiva: 422 `rejected`, no 409.

## POST /api/v1/tickets/{id}/transition

**Alcance:** `tickets:write`, con rol Agente y acceso al ticket. `id` entero positivo.
Cuerpo: cadena `state` (estado de destino) y cadena opcional `reason`.

```json
{"state":"in_progress","reason":"Investigating the network outage"}
```

Se aplica la misma máquina de estados que en la interfaz. Reabrir de `closed` a `in_progress`
requiere un motivo no vacío. Un ticket `resolved` con solicitante no puede pasar manualmente
a `closed`: el solicitante debe usar `/confirm`.
**Respuesta:** 200, el detalle completo anterior con el nuevo estado y la cronología;
no requiere además `tickets:read`. Rol Usuario o cierre prohibido: 403; identificador fuera
del acceso permitido: 404; transición inválida o falta de motivo de reapertura: 422 `rejected`.

## POST /api/v1/tickets/{id}/comments

**Alcance:** `comments:write` para comentarios públicos, `comments:internal` para internos.
`id` entero positivo. Cuerpo: cadena obligatoria no vacía `body` y cadena opcional `visibility`
(`public`, por defecto, o `internal`).

```json
{"body":"Only the second floor is affected.","visibility":"public"}
```

**Respuesta:** 201, el comentario nuevo, no el detalle del ticket:

```json
{"id":7,"author":{"id":12,"name":"Monitoring"},"visibility":"public","body":"Only the second floor is affected.","created_at":"2026-10-23T09:01:00Z"}
```

Se recortan los espacios de los extremos del cuerpo. Los comentarios internos requieren un
agente. Los tickets cerrados o cancelados rechazan comentarios; un ticket resuelto solo permite
comentar a su solicitante mientras espera confirmación. Una respuesta pública del solicitante
reanuda un ticket en espera. Falta del alcance elegido o comentario prohibido: 403; identificador
fuera del acceso permitido: 404; cuerpo vacío o visibilidad inválida: 422 `validation`.

## POST /api/v1/tickets/{id}/confirm

**Alcance:** `tickets:write`. `id` entero positivo. Solo el solicitante del ticket puede
confirmar su resolución. El cuerpo es un objeto JSON vacío:

```json
{}
```

**Respuesta:** 200, el detalle completo anterior, ahora con `state: "closed"` y la cronología
actualizada. No requiere además `tickets:read`. Ticket legible pero solicitante incorrecto:
403; identificador fuera del acceso permitido: 404; transición inválida: 422 `rejected`.

## POST /api/v1/tickets/{id}/reject

**Alcance:** `tickets:write`. `id` entero positivo. Solo el solicitante del ticket puede
rechazar su resolución. El cuerpo es un objeto JSON vacío:

```json
{}
```

**Respuesta:** 200, el detalle completo anterior, ahora con `state: "in_progress"`. El rechazo
desvincula el flujo: `workflow` es null y el ticket continúa de forma manual. No requiere
además `tickets:read`. Ticket legible pero solicitante incorrecto: 403; identificador fuera
del acceso permitido: 404; transición inválida: 422 `rejected`.

## Auditoría

Las acciones mediante la API se atribuyen al usuario de servicio. Los eventos de auditoría
registran también el identificador de la clave (`api_key_id`). Los comentarios ordinarios
**no crean un evento de auditoría**: su procedencia es su autor, el usuario de servicio.
Si un comentario produce un evento, por ejemplo al reanudar una espera, ese evento registra
el identificador de la clave. La cronología del detalle es una proyección limitada, no una
copia de todos los campos de auditoría almacenados.

## Recorrido con curl

Este ejemplo usa una clave de un usuario de servicio con rol Usuario y alcances `catalog:read`,
`tickets:read`, `tickets:write` y `comments:write`. `TKT_TOKEN` debe contener el token emitido
en el entorno local, sin guardarlo en el repositorio. La URL y los identificadores del ejemplo
se reemplazan por los de la instancia.

```sh
TKT_URL='https://tkt.example.com'

curl -sS "$TKT_URL/api/v1/me" -H "Authorization: Bearer $TKT_TOKEN"
curl -sS "$TKT_URL/api/v1/categories" -H "Authorization: Bearer $TKT_TOKEN"

curl -sS -i "$TKT_URL/api/v1/tickets" \
  -H "Authorization: Bearer $TKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"category_id":3,"title":"Network unavailable","description":"The office network is unavailable.","priority":"medium"}'

curl -sS "$TKT_URL/api/v1/tickets/42/comments" \
  -H "Authorization: Bearer $TKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"body":"Only the second floor is affected.","visibility":"public"}'
```

Si la categoría pide información del solicitante, se consulta el ticket nuevo y se completa
su formulario pendiente mediante el endpoint de flujo antes de esperar la intervención del equipo.
