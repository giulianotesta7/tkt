# Servidor MCP

[English](../docs/mcp.md) | **Español**

Asistentes de IA como Claude, Claude Code, VS Code y Cursor pueden trabajar en tkt mediante el
Model Context Protocol (MCP). Cada persona conecta su propio asistente, inicia sesión y lo
autoriza una vez. A partir de ahí el asistente actúa **en nombre de esa persona**: ve y hace
solo lo que la persona podría hacer en tkt, limitado además por los permisos que autorizó. El
endpoint es `<URL pública>/mcp`; root lo enciende en **Configuración → API**. Está apagado por
defecto.

## Recorrido rápido

1. Root configura la URL pública y enciende **Clientes MCP** ([Habilitar MCP](#habilitar-mcp)).
2. Cada persona agrega `<URL pública>/mcp` a su asistente ([Conectar un cliente](#conectar-un-cliente)).
3. El asistente abre tkt en el navegador. Se inicia sesión, se revisa la solicitud y se elige
   **Permitir acceso**.
4. Se le pide al asistente algo como «¿cuáles son mis tickets abiertos?». Usa la herramienta `search_tickets`.

## Cómo funciona

| Tema | Regla |
| --- | --- |
| Identidad | El asistente actúa como la persona que lo autorizó. Los tickets, comentarios y registros de auditoría nombran a esa persona. Un usuario de servicio nunca puede autorizar un cliente. |
| Permisos | El permiso efectivo es **rol ∩ alcances autorizados**. Un alcance nunca da más de lo que permite el rol de la persona, y un permiso del rol sin su alcance se rechaza. |
| Qué puede hacer | Diez herramientas que ejecutan dentro de tkt el endpoint correspondiente de la [API v1](api.md), con las mismas reglas y errores. No hay nada administrativo. |
| Qué no puede hacer | Sin recursos, prompts ni notificaciones. Sin acceso a toda la cola con `tickets:all`. |
| Transporte | Streamable HTTP sin sesiones: cada solicitud es un `POST` a `/mcp` con un token Bearer. |
| Autorización | tkt es su propio servidor de autorización OAuth 2.1. Los clientes se registran solos, usan PKCE y reciben tokens válidos solo para `/mcp`. |

El interruptor de MCP es independiente del de la API: `/mcp` funciona con la API apagada, y la
API no depende de MCP.

## Habilitar MCP

Solo root enciende o apaga los clientes MCP. Los administradores ven su estado.

1. Completar la **Dirección de TKT** en **Configuración → Notificaciones por email** con la
   dirección que usan las personas, por ejemplo `https://tkt.example.com`. Ver
   [La URL base pública](deployment.md#la-url-base-pública).
2. Abrir **Configuración → API** y encender **Clientes MCP**. Sin URL pública, tkt lo rechaza
   con «Los clientes MCP necesitan la URL pública».
3. Compartir la URL de conexión que se muestra ahí, `<URL pública>/mcp`, con las personas que
   van a conectarse.

La URL pública es el emisor OAuth y la audiencia de todo token. **Cambiarla desconecta a todos
los clientes**: los tokens emitidos para la dirección `/mcp` anterior se rechazan y cada persona
vuelve a conectarse.

Detrás de un [proxy inverso](deployment.md#proxy-inverso-con-https), estas rutas tienen que
llegar a tkt sin cambios:

| Ruta | La usa |
| --- | --- |
| `/mcp` | El endpoint MCP. |
| `/oauth/register`, `/oauth/token` | Registro de clientes y tokens; los llama el asistente. |
| `/oauth/authorize` | La pantalla de consentimiento, en el navegador de la persona. |
| `/.well-known/oauth-protected-resource`, `/.well-known/oauth-protected-resource/mcp`, `/.well-known/oauth-authorization-server` | Documentos de descubrimiento. |

Con el interruptor apagado, o con la URL pública vacía, todas responden 404 y los tokens
existentes dejan de funcionar. Las autorizaciones no se revocan: al volver a encenderlo, los
tokens no vencidos vuelven a funcionar. Para cortar un cliente de forma definitiva, se revoca
([Revocar el acceso](#revocar-el-acceso)).

## Conectar un cliente

En los ejemplos, `https://tkt.example.com/mcp` se reemplaza por la URL de conexión,
`<URL pública>/mcp`. No se configura a mano ningún token, ID de cliente ni secreto: el
asistente se registra solo la primera vez que se conecta y abre tkt en el navegador para la
autorización.

| Cliente | Cómo agregar tkt |
| --- | --- |
| Claude (claude.ai y la app de escritorio) | Agregar un conector personalizado con la URL `https://tkt.example.com/mcp`. |
| Claude Code | `claude mcp add --transport http tkt https://tkt.example.com/mcp` |
| VS Code | Agregar el servidor a `.vscode/mcp.json` (abajo). |
| Cursor | Agregar el servidor a `~/.cursor/mcp.json` (abajo). |

VS Code, `.vscode/mcp.json`:

```json
{"servers":{"tkt":{"type":"http","url":"https://tkt.example.com/mcp"}}}
```

Cursor, `~/.cursor/mcp.json`:

```json
{"mcpServers":{"tkt":{"url":"https://tkt.example.com/mcp"}}}
```

> [!NOTE]
> Dónde guarda cada cliente esta configuración depende de su fabricante y puede cambiar entre
> versiones. tkt solo exige que el cliente admita MCP remoto por Streamable HTTP con OAuth y
> registro dinámico de clientes.

## La pantalla de consentimiento

Cuando el asistente se conecta, el navegador abre tkt en `/oauth/authorize`. Una persona sin
sesión iniciada pasa antes por la página de inicio de sesión, con contraseña o con la cuenta de
la empresa, y después vuelve a la pantalla de consentimiento.

La pantalla pregunta **«¿Permitir que <nombre del cliente> actúe en tu nombre?»** y muestra:

- la persona con sesión iniciada;
- **Dirección de retorno**: adónde vuelve el navegador después de decidir. Solo se debe permitir
  una aplicación que la propia persona empezó a conectar;
- **Qué puede hacer**: los alcances solicitados.

**Permitir acceso** devuelve el navegador al asistente con un código de un solo uso. **Denegar**
lo devuelve con `error=access_denied` y no se otorga nada.

| Alcance | Lo que ve la persona |
| --- | --- |
| `catalog:read` | Ver las categorías y sus formularios |
| `tickets:read` | Leer y buscar los tickets que puedes ver |
| `tickets:write` | Crear tickets y trabajar en ellos en tu nombre |
| `comments:write` | Escribir comentarios públicos en tu nombre |
| `comments:internal` | Escribir comentarios internos en tu nombre |

Un cliente que no pide alcances pide los cinco. Al autorizar, se descartan los alcances que el
rol de la persona no puede usar; hoy es `comments:internal` para el rol **Usuario**.

Si el cliente o su dirección de retorno no están registrados, tkt muestra **«Esta conexión no
puede continuar»** y nunca devuelve el navegador al cliente. Hay que volver a conectar el
asistente desde cero.

## Herramientas

Cada herramienta ejecuta su endpoint de la [API v1](api.md) en nombre de la persona. Las
entradas replican el cuerpo o los parámetros de consulta del endpoint; `ticket_id` y la
`position` del paso pasan de la ruta a la entrada. La referencia de la API tiene las reglas
completas de cada campo y las formas de respuesta.

| Herramienta | Alcance | Endpoint de la API v1 | Entrada |
| --- | --- | --- | --- |
| `whoami` | ninguno | `GET /api/v1/me` | ninguna |
| `list_categories` | `catalog:read` | `GET /api/v1/categories` | ninguna |
| `search_tickets` | `tickets:read` | `GET /api/v1/tickets` | opcionales `q`, `state` (arreglo), `cursor` |
| `get_ticket` | `tickets:read` | `GET /api/v1/tickets/{id}` | `ticket_id` |
| `create_ticket` | `tickets:write` | `POST /api/v1/tickets` | `category_id`, `title`, `priority`; opcional `description` |
| `complete_workflow_step` | `tickets:write` | `POST /api/v1/tickets/{id}/workflow/steps/{position}/complete` | `ticket_id`, `position`; opcionales `answers`, `solution` |
| `transition_ticket` | `tickets:write` | `POST /api/v1/tickets/{id}/transition` | `ticket_id`, `state`; opcional `reason` |
| `add_comment` | `comments:write`, o `comments:internal` cuando `internal` es verdadero | `POST /api/v1/tickets/{id}/comments` | `ticket_id`, `body`; opcional `internal` (booleano) |
| `confirm_resolution` | `tickets:write` | `POST /api/v1/tickets/{id}/confirm` | `ticket_id` |
| `reject_resolution` | `tickets:write` | `POST /api/v1/tickets/{id}/reject` | `ticket_id` |

- `ticket_id` es el ID que devuelve `search_tickets`, no el número visible del ticket.
- `whoami` responde la persona: `id`, `name`, `role` y los `scopes` autorizados.
- `search_tickets` devuelve páginas de diez; `next_cursor` se envía de vuelta como `cursor` con
  los mismos filtros.
- `add_comment` envía `internal: true` como `visibility: "internal"`; si no, el comentario es público.
- Una herramienta correcta devuelve la respuesta JSON del endpoint, como texto y como contenido
  estructurado.

## Errores

Los errores a nivel HTTP detienen la solicitud antes de ejecutar cualquier herramienta:

| Estado | Cuerpo | Cuándo |
| --- | --- | --- |
| 404 | `{"error":"not_found"}` | Los clientes MCP están apagados o la URL pública está vacía. |
| 403 | `{"error":"forbidden"}` | Hay una cabecera `Origin` y su esquema y host difieren de la URL pública. |
| 401 | `{"error":"unauthorized"}` | No hay token Bearer. |
| 401 | `{"error":"invalid_token"}` | El token está mal formado, es desconocido, venció, fue revocado, se emitió para otra dirección o su persona está desactivada. |
| 429 | `{"error":"rate_limited"}` | La conexión agotó sus solicitudes; esperar `Retry-After` segundos. |
| 403 | `{"error":"insufficient_scope"}` | Un `tools/call` necesita un alcance que la autorización no incluye. |

Las dos respuestas 401 llevan el desafío que inicia el descubrimiento; un token inválido agrega
`error="invalid_token"`:

```http
WWW-Authenticate: Bearer resource_metadata="https://tkt.example.com/.well-known/oauth-protected-resource/mcp", scope="catalog:read tickets:read tickets:write comments:write comments:internal"
```

El desafío 403 `insufficient_scope` pide los alcances ya autorizados más el que falta, así que
volver a autorizar no quita ningún alcance:

```http
WWW-Authenticate: Bearer error="insufficient_scope", scope="tickets:read tickets:write", resource_metadata="https://tkt.example.com/.well-known/oauth-protected-resource/mcp"
```

Cuando el endpoint rechaza la operación, el resultado de la herramienta tiene `isError: true` y
su texto es el título, el estado, el detalle y los errores por campo del
[problema](api.md#detalles-de-problemas):

```text
Invalid request (422): unknown ticket state
- state: unknown ticket state
```

## Tokens y sesiones

| Elemento | Duración | Notas |
| --- | --- | --- |
| Código de autorización | 60 segundos | Un solo uso. Ligado al cliente, la dirección de retorno, el verificador PKCE y `/mcp`. |
| Token de acceso (`tkta_…`) | 1 hora | Se envía como `Authorization: Bearer` solo en `/mcp`. |
| Token de refresco (`tktr_…`) | 30 días de inactividad | Se reemplaza en cada uso; cada refresco reinicia los 30 días. |

Un token de refresco sirve una sola vez. Presentar uno ya usado revoca toda la autorización:
tkt lo trata como robado o repetido, y la persona vuelve a conectar el asistente. tkt guarda
solo hashes SHA-256 de códigos y tokens.

## Revocar el acceso

| Acción | Efecto |
| --- | --- |
| **Preferencias → Aplicaciones conectadas**, **Revocar** | Termina una autorización en el acto. Cada persona ve y revoca solo sus propias conexiones: nombre del cliente, alcances, creación y último uso. |
| Desactivar a la persona | Revoca todas sus autorizaciones. Reactivarla no las recupera. |
| Apagar **Clientes MCP** | Toda solicitud responde 404 hasta volver a encenderlo; las autorizaciones se mantienen. |
| Cambiar la URL pública | Se rechaza todo token; cada persona vuelve a conectarse. |

No hay una vista de administración con las conexiones de todas las personas.

## Notas de seguridad

- **Registro.** Los clientes se registran solos con registro dinámico de clientes (RFC 7591)
  como clientes públicos, sin secreto. No se admiten documentos de metadatos de ID de cliente
  ni clientes registrados de antemano. El registro es anónimo, así que tiene un límite para
  toda la instancia de 30 seguidos y después uno cada dos segundos (429 con `Retry-After`), y
  un cliente registrado que nadie autoriza en 24 horas se elimina.
- **Direcciones de retorno.** Un cliente puede registrar hasta 10: `https` con host, `http` en
  `localhost`, `127.0.0.1` o `[::1]`, o un esquema de aplicación como `cursor://`. Se rechazan
  los esquemas web, de script y de archivo (`javascript:`, `data:`, `file:` y similares) y los
  fragmentos. Para `http` en un host de loopback se ignora el puerto al comparar, porque los
  clientes de escritorio eligen un puerto libre cada vez.
- **PKCE y state.** Solo se acepta `S256`, y `state` es obligatorio. La redirección después de
  decidir lleva `iss` (RFC 9207) para que el cliente confirme que viene de este tkt.
- **Audiencia.** Los tokens están ligados a `<URL pública>/mcp`. La API los rechaza con 401, y
  `/mcp` rechaza las claves de la API.
- **Auditoría.** Los cambios hechos por MCP registran a la persona como actor y la autorización
  como procedencia (`oauth_grant_id`), igual que los cambios por la API registran `api_key_id`.
- **Límite de frecuencia.** Cada autorización tiene un límite de **60 solicitudes por minuto**
  en `/mcp` (reserva de 60, una más por segundo), contado por solicitud HTTP. El límite es
  local al proceso y se reinicia al reiniciar el servidor.

## Endpoints de descubrimiento

Los clientes encuentran todo a partir del desafío de `/mcp`; esta lista sirve para diagnosticar
problemas. Todos responden 404 con los clientes MCP apagados.

| Endpoint | Contenido |
| --- | --- |
| `GET /.well-known/oauth-protected-resource/mcp` (también sin `/mcp`) | RFC 9728: `resource` (`<URL pública>/mcp`), `authorization_servers`, `scopes_supported`, `bearer_methods_supported: ["header"]`. |
| `GET /.well-known/oauth-authorization-server` | RFC 8414: `issuer` (la URL pública), los endpoints de autorización, token y registro, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `grant_types_supported: ["authorization_code","refresh_token"]`. |
| `POST /oauth/register` | Registro en JSON; 201 con `client_id`. |
| `POST /oauth/token` | Concesiones `authorization_code` y `refresh_token` codificadas como formulario; las respuestas usan `Cache-Control: no-store`. |

## Próximos pasos

- [Leer la referencia de la API v1](api.md) con los campos y respuestas de cada herramienta
- [Administrar los clientes MCP](administration.md#clientes-mcp)
