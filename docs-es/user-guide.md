# Guía del usuario

[English](../docs/user-guide.md) | **Español**

Un ticket tiene dos lados: la persona que reporta un problema y el equipo que lo trabaja. Lo que
puede hacer cada rol está en [Administración](administration.md#roles).

## Reportar un problema

### Tus tickets

**Tickets** abre **Mis tickets**, agrupados por lo que necesita cada solicitud:

- **Necesitan tu acción**: tickets en espera de tu respuesta y tickets resueltos que esperan tu
  confirmación.
- **Abiertos**: solicitudes que todavía se están trabajando.
- **Cerrados**: plegados al final.

Un punto junto a un título significa que algo cambió desde la última vez que abriste ese ticket.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../docs/images/my-tickets-dark.webp">
  <img src="../docs/images/my-tickets-light.webp" alt="tkt Mis tickets: solicitudes agrupadas por lo que necesitan de vos" width="1440">
</picture>

### Crear un ticket

**Nuevo ticket** pide un título y una descripción, además de:

- **Categoría**: a dónde corresponde la solicitud. Según la instancia, la elegís en la misma
  página o después de elegir en el catálogo.
- **Prioridad**: Baja, Media, Alta o Crítica. Ordena la cola.
- **Archivos adjuntos**: hasta 5 archivos de 10 MB cada uno. El archivo tiene que ser del tipo que
  declara su nombre; un archivo de texto renombrado a `.png` se rechaza.

Si la instancia ofrece sugerencias de categoría, son una pista: hasta tres, nunca se aplican solas
y el catálogo sigue disponible.

Una categoría aparece en el selector solo después de publicar su flujo, así que un selector vacío
significa que todavía no hay ninguna categoría publicada.

### Responder, poner en espera y confirmar

- Mientras el ticket está abierto, escribí en el compositor y elegí **Responder**. Tu respuesta es
  pública y el equipo recibe una notificación.
- Si el equipo pone el ticket **En espera**, el ticket muestra qué necesitan de vos. Tu respuesta
  termina la espera.
- Cuando el equipo marca el ticket como **Resuelto**, el ticket pregunta **¿Se resolvió tu
  problema?**. Elegí **Sí, cerrar el ticket**, o **No, todavía necesito ayuda** para devolverlo.
  Una resolución rechazada reabre el ticket como ticket manual, así que un flujo no puede cerrarlo
  a tus espaldas.
- Los tickets resueltos y cerrados no tienen caja de respuesta.

Tu lista usa las etiquetas **Recibido**, **En curso**, **Resuelto**, **Cerrado** y **Cancelado**.

## Trabajar en tickets

### Tu cola

**Mi trabajo** lista lo que podés atender:

- **Asignados a mí**
- **Disponibles para tomar**: tickets que esperan en una mesa tuya, cuando el flujo usa
  **Autoasignación**. Elegí **Tomar ticket** para tomar uno.
- **Resueltos y cerrados**

Un flujo **Por menor carga** asigna el ticket a un miembro disponible de la mesa, así que nunca
espera en la lista de disponibles.

### Trabajar un ticket

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../docs/images/ticket-detail-dark.webp">
  <img src="../docs/images/ticket-detail-light.webp" alt="tkt detalle del ticket: descripción, actividad, propiedades y el paso actual del flujo" width="1440">
</picture>

- **Estado** avanza por **Empezar**, **Resolver**, **Cancelar** y **Reabrir**, y ofrece solo las
  acciones válidas para ese ticket.
- **Responder** es público: el solicitante lo lee y recibe una notificación. **Nota interna** es
  solo para el personal y nunca se muestra al solicitante, tampoco sus adjuntos.
- **Archivos adjuntos** siguen la regla del comentario al que pertenecen: un archivo de una nota
  interna es solo para el personal y cualquier otro archivo lo ve quien pueda abrir el ticket.
- **Propiedades** cambia la prioridad y permite reasignar cuando hay permiso. Reasignar a otra
  persona pide un motivo; tomar un ticket sin asignar, no.

### Poner en espera, resolver y cerrar

- **Poner en espera** estaciona un ticket que espera al solicitante. La persona ve el motivo que
  escribís, el reloj de resolución se pausa y el de primera respuesta sigue corriendo.
  **Reanudar** termina la espera, y también lo hace una respuesta pública del solicitante.
- **Resolver** pide confirmación al solicitante, como se explica más arriba.
- Un ticket con solicitante no se puede cerrar a mano desde el estado resuelto: solo lo termina la
  confirmación del solicitante o un paso del flujo que cierre el ticket. Cancelar termina un ticket
  que nadie va a trabajar y no se puede reabrir.
- Reabrir un ticket cerrado necesita un motivo y lo devuelve a **En curso**.

### Pasos del flujo

Un flujo publicado mueve el ticket, y sus pasos están descritos en
[Administración](administration.md#flujos-de-trabajo).

## Notificaciones y preferencias

La campana reúne lo que pasó en los tickets que podés ver: asignaciones, comentarios nuevos,
cambios de estado, avisos de SLA, esperas y respuestas de resolución.

**Preferencias** guarda el orden de tu lista, el tema Claro, Oscuro o Sistema, si también recibís
notificaciones por email (activado por defecto y útil solo si la instancia tiene email) y tu propia
contraseña cuando la instancia lo permite.

Cambiar la contraseña cierra las demás sesiones; la sesión desde la que la cambiás sigue abierta.

## Si olvidaste tu contraseña

Con el email configurado y la opción activada, la página de inicio de sesión incluye
**¿Olvidaste tu contraseña?**. Escribí tu correo y tkt envía un enlace de un solo uso que vence en
una hora. La respuesta es la misma tenga o no la dirección una cuenta.

El enlace te deja elegir una contraseña nueva, cierra la sesión de la cuenta en todos lados y te
pide iniciar sesión otra vez. Si se envían dos solicitudes para la misma cuenta casi al mismo
tiempo, pueden llegar ambos mensajes y solo funciona el enlace más reciente; los detalles están en
[Administración → Seguridad](administration.md#seguridad).

> [!NOTE]
> Si no llega ningún mensaje, la instancia no ofrece la recuperación o no tiene un servidor de
> correo que funcione.
