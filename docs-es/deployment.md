# Despliegue

[English](../docs/deployment.md) | **Español**

Ejecutar tkt como contenedor: la imagen, el volumen de datos, la configuración, la URL base
pública y un proxy inverso con HTTPS. La primera instalación está en
[Primeros pasos](getting-started.md).

## La imagen de contenedor

El `Dockerfile` compila el servidor como binario estático y lo copia a una base distroless. La
imagen define el contrato de ejecución, así que todas las formas de ejecutarla comparten los
mismos valores por defecto:

| Propiedad | Valor |
| --- | --- |
| Escucha en | `:8080` (`TKT_LISTEN`), puerto `8080` expuesto |
| Base de datos | `/data/tkt.db` (`TKT_DB_PATH`) |
| Se ejecuta como | UID `65532`, una cuenta sin privilegios |
| Directorio de datos | `/data`, creado y propiedad de `65532` en la imagen |
| Señal de parada | `SIGTERM`, manejada como apagado ordenado |
| Healthcheck | `/server -healthcheck` cada 10 segundos, timeout de 3 segundos, 3 reintentos, período de arranque de 5 segundos |

No hay shell ni gestor de paquetes dentro: inspeccioná el contenedor en lugar de ejecutar
comandos en él.

Tags: un tag `vX.Y.Z` publica `ghcr.io/giulianotesta7/tkt` con la versión completa, la versión
menor y `latest`. Nada se publica desde la rama main, así que `latest` solo avanza con una
versión, y mientras no exista un tag compilás la imagen desde un checkout.

## Ejecutarla

### Con Compose

```console
docker compose up -d --build
```

Publica `8080` en el host, monta el volumen de Compose `tkt-data` en `/data` y deja la política
de reinicio en `unless-stopped`. La imagen es
`ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}`: definí `TKT_TAG` para fijar una versión. Con el
nombre de proyecto predeterminado `tkt`,
Docker llama al volumen `tkt_tkt-data`; el ejemplo con `docker run` de abajo crea otro volumen
llamado `tkt-data`.

El mapeo `8080:8080` publica en todas las interfaces, cómodo para una prueba local. Detrás de un
proxy inverso, cambialo a `127.0.0.1:8080:8080` para que el puerto HTTP plano sea alcanzable
solo desde el proxy.

### Con `docker run`

```console
docker volume create tkt-data
docker build -t tkt:local .
docker run -d \
  --name tkt \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v tkt-data:/data \
  tkt:local
```

## Comprobar que corre

```console
docker ps
curl -fsS http://127.0.0.1:8080/healthz
```

`/healthz` responde `ok` sin autenticación. El healthcheck del contenedor prueba que la base de
datos responde: ejecuta el binario del servidor con `-healthcheck`, que abre la base de datos y
hace una consulta, y `docker ps` muestra `healthy` cuando pasa.

## El volumen de datos

Todo lo que tkt debe conservar vive en `/data`:

| Archivo | Contiene | Backupear |
| --- | --- | --- |
| `tkt.db` | Tickets, personas, el catálogo y los ajustes | Sí, junto con `smtp.key` |
| `smtp.key` | La clave que sella los secretos guardados, escrita en el primer guardado de una contraseña SMTP o de una clave de modelo de decisión | Sí, junto con `tkt.db` |

> [!WARNING]
> Restaurar `tkt.db` sin `smtp.key` deja los secretos guardados ilegibles.
> [Backup y actualizacion](backup-and-upgrade.md#qué-backupear) tiene el procedimiento.

La base de datos usa write-ahead logging, así que `tkt.db-wal` y `tkt.db-shm` pueden aparecer a
su lado mientras el servidor corre. Un volumen con nombre hereda la propiedad preparada de
`/data`, así que el UID sin privilegios puede crear la base de datos en el primer arranque. Un
bind mount no lo hace: el directorio del host debe ser escribible por el UID `65532`.

## Configuración

tkt lee dos variables de entorno. La imagen define ambas, así que definirlas solo cambia el valor
por defecto:

| Variable | Valor por defecto de la imagen | Significado |
| --- | --- | --- |
| `TKT_DB_PATH` | `/data/tkt.db` | Ruta del archivo de la base de datos SQLite |
| `TKT_LISTEN` | `:8080` | Dirección en la que escucha el servidor HTTP |

Todo lo demás se configura en la interfaz y se guarda en la base de datos:

| Pantalla | Configura |
| --- | --- |
| Configuración → Organización | Nombre de la instancia, nombre de la mesa de ayuda, correo de soporte, logo |
| Configuración → Página de inicio de sesión | La presentación de la página de inicio de sesión |
| Configuración → Idioma | El idioma de interfaz de toda la instancia |
| Configuración → Tickets y SLA | Apariencia, SLA activado o desactivado, cierre automático de tickets en espera |
| Configuración → Notificaciones por email (solo root) | Servidor SMTP, cifrado, remitente, Dirección de TKT |
| Configuración → Horario laboral | Zona horaria, días y horas laborables sobre los que corre el reloj del SLA |
| Configuración → Seguridad | Longitud mínima de contraseñas nuevas, cambio y recuperación de contraseña de autoservicio |

[Administración](administration.md) explica qué hace cada ajuste.

### Flag de CLI

El binario del servidor acepta dos flags de una sola ejecución:

- `-healthcheck` abre la base de datos, hace una consulta y sale con `0` o `1`. Lo usa el
  `HEALTHCHECK` de la imagen.
- `-recover-root <id>` activa la cuenta con ese id y la promueve a root en una base de datos cuyo
  root no se puede demostrar, audita la recuperación y sale. Falla cerrado cuando ya existe un
  root o el id es desconocido, y no es un restablecimiento de contraseña general.

Ejecutá un comando de una sola vez con el servidor detenido:

```console
docker stop tkt
docker run --rm -v tkt-data:/data --entrypoint /server tkt:local -recover-root 1
docker start tkt
```

Reemplazá `1` por el id de la cuenta correspondiente, o usá el volumen y la imagen del servicio de
Compose. Si la recuperación falla, investigá el error antes de volver a iniciar el servidor.

## La URL base pública

Los enlaces de los emails tienen que ser absolutos, así que tkt necesita la dirección con la que
la gente lo alcanza: el campo **Dirección de TKT** de Configuración → Notificaciones por email.
Es obligatorio cuando las notificaciones por email están activas, y el valor guardado se valida
como URL `http` o `https` absoluta y se almacena sin barra final.

Esa dirección se usa en los enlaces de ticket de los emails de notificación y en el enlace de un
solo uso del mensaje de recuperación de contraseña. Ponela en la dirección pública, por ejemplo
`https://tkt.example.com`, nunca en `localhost`.

## Proxy inverso con HTTPS

tkt habla HTTP plano y no termina TLS, así que el proxy de adelante tiene el certificado.

1. Preservá el header `Host` original, puerto incluido: tkt lo compara con el header `Origin` del
   navegador en las peticiones que cambian estado y rechaza la diferencia con `403`. `$http_host`
   de nginx mantiene el puerto y `$host` lo pierde.
2. Limitá el puerto del contenedor a la interfaz del proxy, `127.0.0.1` o una red privada.
3. Definí la Dirección de TKT como la dirección HTTPS pública, para que los enlaces de los emails
   nunca apunten a la dirección interna del proxy.

Un server block de nginx:

```nginx
server {
    listen 443 ssl;
    server_name tkt.example.com;

    ssl_certificate     /etc/ssl/certs/tkt.example.com.pem;
    ssl_certificate_key /etc/ssl/private/tkt.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}

server {
    listen 80;
    server_name tkt.example.com;
    return 301 https://$host$request_uri;
}
```

> [!WARNING]
> El servidor ve HTTP plano y no puede marcar la cookie de sesión como `Secure`; la cookie es
> `HttpOnly` y `SameSite=Strict`. Activá HSTS en el proxy y nunca expongas el contenedor directo a
> internet.
