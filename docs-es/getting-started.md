# Primeros pasos

[English](../docs/getting-started.md) | **Español**

De un host vacío al primer ticket: instalar tkt, crear la cuenta root y aplicar la
configuración mínima. El contrato del contenedor y los ajustes soportados están en
[Despliegue](deployment.md); la administración del día a día, en
[Administración](administration.md).

## Antes de empezar

- Docker y Docker Compose.
- Un puerto accesible desde el navegador. Los ejemplos usan `8080`, el puerto donde escucha el
  contenedor.

También podés ejecutarlo desde el código fuente; ver [CONTRIBUTING](../CONTRIBUTING.md).

## 1. Iniciar tkt

```console
docker compose up -d --build
```

Compose compila la imagen, publica `8080` y guarda los datos en el volumen `tkt-data`;
[Despliegue](deployment.md#el-volumen-de-datos) cubre el volumen y su propiedad.

```console
docker compose ps
curl -fsS http://localhost:8080/healthz
```

`/healthz` responde `ok`; [Despliegue](deployment.md#comprobar-que-corre) cubre el healthcheck del
contenedor.

## 2. Crear la cuenta root

Abrí `http://localhost:8080/setup`. El formulario existe solo hasta que se crea la primera cuenta;
las visitas posteriores redirigen a la página de inicio de sesión.

- **Nombre**: cómo aparece la cuenta en los tickets y en la traza de auditoría.
- **Contraseña**: 12 caracteres como mínimo por defecto y 72 bytes como máximo. El mínimo se
  configura más adelante, entre 8 y 64 caracteres.

Iniciá sesión con esas credenciales.

## 3. Configuración mínima

Iniciá sesión como root.

1. **Configuración → Organización**: el nombre del encabezado de la interfaz, el nombre de la mesa
   de ayuda y el correo de soporte.
2. **Configuración → Idioma**: un solo idioma de interfaz para toda la instancia, aplicado al
   instante.
3. **Catálogo**: un departamento agrupa mesas, una mesa agrupa categorías y a sus agentes, y una
   categoría agrupa solicitudes que siguen un mismo flujo. Creá un departamento, una mesa y una
   categoría, abrí el flujo de la categoría, agregá un paso y publicalo.
4. **Usuarios**: creá las cuentas que trabajan en tickets, dales el rol **Agente** y sumalas a la
   mesa. Quien solo reporta problemas necesita el rol **Usuario** y ninguna mesa.
5. **Configuración → Seguridad**: la longitud mínima de las contraseñas nuevas y los dos
   interruptores de autoservicio (cambio desde Preferencias y recuperación en la página de inicio
   de sesión); la recuperación necesita notificaciones por email.
   [Administración](administration.md#seguridad) cubre las reglas.
6. **Configuración → Notificaciones por email** (solo root, opcional): un servidor SMTP, una
   dirección de remitente y la **Dirección de TKT**;
   [Despliegue](deployment.md#la-url-base-pública) explica la dirección.

Comprobá abriendo **Nuevo ticket**: la categoría publicada está en el selector.

## 4. Crear un primer ticket

Cerrá sesión, entrá como alguien con el rol **Usuario**, abrí **Nuevo ticket**, elegí la
categoría publicada, escribí un título y una descripción, y enviá.

Comprobá que el ticket aparece en la lista de esa persona y que los miembros de la mesa lo ven en
su cola a medida que el flujo lo asigna.

## Próximos pasos

- [Despliegue](deployment.md): la imagen, el volumen de datos, la URL base pública y HTTPS
  detrás de un proxy inverso.
- [Administración](administration.md): usuarios y roles, mesas, categorías, flujos, SLA, emails
  y los ajustes de Seguridad.
- [Guía de uso](user-guide.md): los recorridos del solicitante y del agente.
- [Backup y actualizacion](backup-and-upgrade.md): qué backupear, cómo restaurar y
  cómo pasar a una versión nueva.
