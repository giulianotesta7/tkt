# Administración

[English](../docs/administration.md) | **Español**

Cómo se administra una instancia: roles, catálogo, flujos de trabajo, SLA, creación de tickets,
email y seguridad. Primera cuenta: [Primeros pasos](getting-started.md). Contenedor y
configuración: [Despliegue](deployment.md).

## Roles

Cada rol incluye todo lo que hay debajo.

| Rol | Puede hacer |
| --- | --- |
| **Usuario** | Crear tickets, responder públicamente en sus propios tickets y contestar la confirmación de resolución. |
| **Agente** | Todo lo de Usuario, más trabajar en los tickets asignados o en los que esperan en sus mesas: editar, cambiar de estado, asignar y escribir respuestas públicas y notas internas. |
| **Administrador** | Todo lo de Agente, más gestionar usuarios (salvo las cuentas administrador y root), mesas, categorías, flujos de trabajo, métricas y toda la configuración excepto el email. |
| **Root** | Todo, más otorgar y quitar el rol **Administrador** y gestionar el email. |

- Los tickets que se pueden tomar dependen de las mesas de cada persona, así que sumá a cada
  agente a la mesa de su categoría. Los tickets asignados aparecen igual.
- La cuenta root está protegida: nadie más puede editarla, desactivarla ni gestionarla, y root
  cambia su propia contraseña en **Preferencias**.
- Solo root otorga o quita el rol **Administrador**. Ningún administrador puede crear, gestionar
  ni tocar a otro administrador ni a la cuenta root.
- Quien gestiona usuarios puede fijar una contraseña nueva a una cuenta de nivel inferior,
  respetando el mínimo de la instancia.

## Personas

1. Abrí **Usuarios**. Filtrá por **Activos** o **Desactivados**, o buscá por nombre o email.
2. **Nuevo usuario** pide nombre, email y contraseña; la cuenta arranca con el rol **Usuario**.
3. **Desactivar usuario** conserva la cuenta y su historial, impide el inicio de sesión y termina
   las sesiones activas en su próxima solicitud. **Reactivar usuario** lo revierte.

No hay invitaciones ni registro propio: la primera cuenta se crea en `/setup` y pasa a ser root,
y el resto se crea aquí.

## Mesas y catálogo

El catálogo enruta las solicitudes:

```text
Department  →  Desk  →  Category
```

- Un **departamento** agrupa las mesas que atienden la misma parte de la organización.
- Una **mesa** agrupa las categorías de un equipo y contiene a sus miembros.
- Una **categoría** agrupa las solicitudes que siguen el mismo flujo.

Toda instalación arranca con un departamento **General** y una mesa **General**. Los nombres son
únicos en su nivel, y una categoría siempre pertenece a una mesa.

**Catálogo** administra el árbol en el lugar, y **Requiere atención** muestra las mesas sin
agentes y las categorías sin flujo publicado. Los miembros de una mesa necesitan rol Agente o
superior. Eliminar cualquier nodo pide confirmación y no se puede deshacer.

Cada categoría tiene **Palabras clave para sugerencias** para el motor local y objetivos de
**SLA** propios o de la instancia. Su flujo tiene que estar publicado para aceptar tickets.

## Flujos de trabajo

1. Abrí **Configurar flujo** en una categoría y agregá sus pasos.
2. **Guardar** conserva el borrador; **Publicar** lo convierte en la versión con la que corren los
   tickets nuevos.
3. Creá un ticket en la categoría para confirmar que corre el paso publicado.

- Un borrador no cambia los tickets existentes: cada ticket conserva la versión con la que fue
  creado.
- Publicar valida la definición: al menos un paso, claves de campo únicas y como máximo un
  cierre, que debe ser el último paso.
- Publicar rechaza los flujos que nunca podrían mover un ticket, como una asignación Por menor
  carga a una mesa sin agentes disponibles. Si solo falta que una persona asigne el ticket, se
  muestra una advertencia y no un rechazo.
- **Reutilizar un flujo** copia a la categoría actual un flujo publicado de otra categoría sin
  publicarlo.

| Paso | Qué hace |
| --- | --- |
| **Enviar a una mesa** | Enruta el ticket a una mesa. Con **Autoasignación**, lo toman los miembros de la mesa. Con **Por menor carga**, tkt lo asigna al miembro activo con menos tickets abiertos. |
| **Pedir información** | Pide al solicitante o al responsable un texto corto, un texto largo, una casilla o una opción de una lista, cada campo opcionalmente obligatorio. |
| **Dar una tarea al agente** | Da instrucciones al responsable para ejecutarlas y marcarlas como hechas. |
| **Marcar el ticket como resuelto** | Termina el flujo resolviéndolo. Se ejecuta solo. |
| **Cerrar el ticket** | Termina el flujo cerrándolo. Se ejecuta solo. |

## Acuerdos de nivel de servicio

El SLA está desactivado hasta habilitarlo en **Configuración → Tickets y SLA**, que también
guarda los valores de la instancia.

| Ajuste | Por defecto | Efecto |
| --- | --- | --- |
| Objetivos de **Primera respuesta** y de **Resolución** | uno por prioridad, de Baja a Crítica | Se congelan en cada ticket nuevo. |
| **Umbral de aviso** | 80% | Parte del objetivo transcurrida antes de marcar el ticket en riesgo. |
| **Cierre automático de tickets en espera** | desactivado | Desde dos días hábiles, tkt avisa al solicitante un día hábil antes de cerrar el ticket que espera su respuesta y lo cierra si nunca responde. Una ventana de un día cierra sin aviso. |
| **Horario laboral** | lunes a viernes, 09:00 a 18:00, UTC | Zona horaria, días laborables y franja diaria del reloj. |

- Los valores de la instancia no modifican las categorías existentes: cada una conserva sus
  objetivos o los restablece a los de la instancia.
- Las ediciones valen solo para tickets nuevos, y un ticket creado con el SLA desactivado no
  tiene compromiso.
- La espera pausa solo el reloj de resolución; el de primera respuesta sigue corriendo.

## Creación de tickets y sugerencias de categoría

**Configuración → Tickets y SLA** también define cómo se elige la categoría: **Una sola página**
(por defecto) muestra el formulario con el selector, y **Dos pasos** muestra primero el catálogo
y después el formulario con la categoría fija.

| Motor | Comportamiento |
| --- | --- |
| **Desactivadas** (por defecto) | Sin sugerencias; cada persona elige del catálogo. |
| **En este servidor** | Compara el texto con nombres, descripciones y **Palabras clave para sugerencias** de las categorías, y con los nombres de la mesa y el departamento que las contienen. Nada sale de tkt. |
| **Modelo de decisión** | Envía el texto y la lista de categorías a un endpoint externo que implementa `POST /decisions`. |

Las sugerencias son orientativas: hasta tres, las elige la persona y nunca se aplican solas. Sin
una coincidencia con confianza suficiente, el selector sigue siendo el catálogo.

El modelo de decisión recibe **el texto que la persona escribió sobre el problema (título y
descripción, hasta 4.000 caracteres) y el nombre y la descripción de cada categoría ofrecida**.
Nunca recibe el nombre ni el email de quien pide, los adjuntos ni el historial del ticket. La
respuesta se corta a los dos segundos; si el modelo falla, rechaza o queda por debajo de su
umbral de confianza, esa sugerencia sale del motor local.

La URL del endpoint, el modelo y la clave de API se guardan cifrados como la contraseña SMTP.
**Probar ahora** muestra un texto de ejemplo sin guardar nada.

Una prueba del 23 de octubre de 2026 usó 52 categorías y 226 descripciones de problemas en español
e inglés. El acierto indica en cuántas de las 206 descripciones con categoría esperada esta apareció
primera; la latencia cubre la solicitud de sugerencia completa.

| Motor | Acierto en la primera opción | Latencia mediana |
| --- | --- | --- |
| **En este servidor**, con palabras clave | 48% | 17 ms |
| **En este servidor**, sin palabras clave | 14% | 9 ms |
| **Modelo de decisión** (`gpt-6-luna`) | 95% | 326 ms |

La prueba del modelo hizo 219 solicitudes: los 206 casos con categoría y 13 de los 20 sin
coincidencia, antes de detenerse tras 10 derivaciones consecutivas al motor local. El resultado
incluye esas derivaciones y es anterior al manejo actual de casos sin coincidencia; no es una
medición del modelo aislado.

A la [tarifa publicada de Decisions de OpenAI](https://developers.openai.com/api/docs/guides/decisions)
de $0,10 por millón de tokens de entrada sin caché y sin cargo por salida, los 298.634 tokens de
entrada estimados (caracteres divididos por cuatro) costarían unos **$0,03 por la prueba**, o
**$0,14 por cada 1.000 solicitudes similares**. No es un total facturado; excluye escrituras de
caché y recargos regionales.

## Notificaciones por email

Solo root configura el email, en **Configuración → Notificaciones por email**: servidor, puerto
(25 cuando está vacío), cifrado, credenciales y remitente. Sin esa configuración la campana de
notificaciones sigue funcionando; el email agrega una copia de las mismas notificaciones.

| Campo | Por defecto | Efecto |
| --- | --- | --- |
| **Cifrado** | ninguno | STARTTLS o TLS es obligatorio en cuanto hay usuario y contraseña, porque las credenciales nunca se envían sin cifrar. |
| **Usuario** y **Contraseña** | vacíos | Opcionales si el servidor acepta correo sin autenticación. La contraseña guardada no se vuelve a mostrar; dejá el campo vacío para conservarla. |
| **No verificar el certificado** | desactivado | Mantiene la conexión cifrada pero deja de comprobar con quién se habla. |
| **Dirección del remitente** y **Nombre del remitente** | vacíos | El remitente que ven los destinatarios; el nombre es opcional. |
| **Dirección de TKT** | vacía | Dirección pública que usan los enlaces enviados. Ver [Despliegue](deployment.md#la-url-base-pública). |

**Enviar email de prueba** manda un mensaje a la cuenta root con sesión iniciada usando la
configuración guardada. Un proceso en segundo plano entrega las notificaciones cada 15 segundos.

Cada persona puede desactivar el email en **Preferencias**. La contraseña SMTP se sella con el
archivo de clave que cubre [Backup y actualizacion](backup-and-upgrade.md#qué-backupear).

## Seguridad

**Configuración → Seguridad** guarda la política de contraseñas.

| Ajuste | Por defecto | Efecto |
| --- | --- | --- |
| **Longitud mínima de la contraseña** | 12, entre 8 y 64 | Solo contraseñas nuevas. |
| **Permite que cada persona cambie su propia contraseña desde Preferencias** | activado | Desactivado oculta la sección de contraseña salvo en las rutas que usa la administración. |
| **Ofrece «¿Olvidaste tu contraseña?» en la página de inicio de sesión** | activado | Agrega el enlace a la página de inicio de sesión. Sirve una sola vez, vence en una hora, guarda la contraseña nueva y cierra todas las sesiones de la cuenta. Se admite una solicitud por cuenta cada cinco minutos. |

La recuperación responde el mismo mensaje neutro, tenga o no la dirección una cuenta activa.

> [!NOTE]
> La recuperación de contraseña necesita esta opción y el email funcionando.

**Limitación conocida:** el límite de solicitudes y el enlace de un solo uso se aplican por
solicitud, no como un paso atómico entre dos solicitudes. Dos solicitudes para la misma cuenta
casi al mismo tiempo pueden enviar ambos emails, y el enlace de la más nueva invalida el anterior.
Solo funciona el enlace más reciente.

## Próximos pasos

- [Backupear la base de datos y el archivo de clave](backup-and-upgrade.md)
- [Recorrer el recorrido del solicitante y del agente](user-guide.md)
