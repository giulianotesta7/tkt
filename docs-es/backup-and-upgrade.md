# Backup y actualizacion

[English](../docs/backup-and-upgrade.md) | **Español**

Todo lo que tkt conserva vive en su directorio de datos. Backupealo antes de cada actualización y
seguí esta guía para restaurarlo o pasar a una versión nueva. El directorio está descrito en
[Despliegue](deployment.md#el-volumen-de-datos).

## Qué backupear

Copiá el directorio de datos completo, no una selección de archivos.

| Archivo | Por qué importa |
| --- | --- |
| `tkt.db` | La base de datos SQLite: tickets, comentarios, personas, el catálogo, flujos de trabajo, configuración y adjuntos. Los adjuntos están dentro de la base de datos, no como archivos separados. |
| `smtp.key` | La clave que sella la contraseña SMTP guardada y la clave de API del modelo de decisión. Está junto a la base de datos y se crea al guardar el primer secreto. |
| `tkt.db-wal`, `tkt.db-shm` | Los archivos de registro de escritura anticipada y de memoria compartida de SQLite. Pueden existir mientras el servidor corre y son parte del estado de la base de datos. |

`smtp.key` es una cerradura, no una segunda copia de los secretos. Una base de datos restaurada sin
ella conserva todos los tickets y la configuración, pero la contraseña SMTP sellada y la clave de
API del modelo de decisión no se pueden abrir: root las vuelve a ingresar en **Configuración →
Notificaciones por email** y en **Configuración → Tickets y SLA**. Quien tenga la base y la clave
puede leer esos secretos, así que guardá los backups en privado.

> [!WARNING]
> Un backup sin `smtp.key` está incompleto. Si la instancia guardó alguna vez una contraseña SMTP
> o una clave de API del modelo de decisión y no existe ninguna copia de la clave, esos secretos se
> pierden para siempre.

## Backupear una instancia con Compose

Detené el servicio para que nada escriba en la base, copiá el directorio a través del contenedor y
volvé a iniciarlo:

```console
docker compose stop app
container=$(docker compose ps -aq app)
mkdir -p backup
docker cp "$container:/data" backup/
docker compose start app
```

Copiar a través del contenedor evita adivinar el nombre de volumen que generó Compose, por ejemplo
`tkt_tkt-data`. Usá un directorio nuevo por backup para que una ejecución posterior no pise la
anterior.

Comprobá que `tkt.db` esté en la copia:

```console
ls -l backup/data
```

## Backupear una instancia con `docker run`

Un contenedor independiente conserva el nombre de volumen que le diste. Para un contenedor llamado
`tkt`:

Detenelo, copiá el directorio de datos y hacé la misma comprobación de arriba:

```console
docker stop tkt
mkdir -p backup
docker cp tkt:/data backup/
docker start tkt
```

Si el contenedor ya no existe, montá el mismo volumen en un contenedor temporal y copiá desde ahí.
Usá la imagen con la que corre la instancia: `tkt:local` en el ejemplo independiente, o la
etiqueta publicada.

```console
docker create --name tkt-backup -v tkt-data:/data tkt:local
docker cp tkt-backup:/data backup/
docker rm tkt-backup
```

## Restaurar

> [!WARNING]
> Una restauración borra los datos actuales. Los dos procedimientos de abajo reemplazan el volumen
> de datos, así que todo lo que no esté en el backup se pierde, y una restauración desde un
> backup incompleto no se puede deshacer. Confirmá que el backup contiene `tkt.db` y, cuando
> corresponda, `smtp.key`, y mantené la instancia actual detenida hasta verificar la restaurada.

Los comandos de restauración usan GNU tar, el `tar` predeterminado en un host Linux.

### Compose

Dejá que Compose recree el volumen que declara, en lugar de nombrarlo.

```console
docker compose down -v
docker compose create app
container=$(docker compose ps -aq app)
tar --owner=65532 --group=65532 -C backup/data -cf - . | docker cp - "$container:/data"
docker compose start app
```

`docker compose down -v` borra el volumen actual, y por eso el backup va primero.
`docker compose create app` lo crea nuevo con el dueño de `/data` que prepara la imagen.

El flujo de tar fija el dueño en el UID y el GID `65532` de forma explícita, porque los archivos
copiados fuera del contenedor pertenecen a la cuenta que ejecuta Docker; usar solo `docker cp -a`
a la vuelta conservaría ese dueño local. Escribir el dueño en el flujo hace que la restauración sea
determinista.

### `docker run`

Recreá el volumen con el nombre que usa la instancia, llenalo y después iniciá el contenedor sobre
él. Usá la misma imagen que el asistente de backup.

```console
docker stop tkt
docker rm tkt
docker volume rm tkt-data
docker volume create tkt-data
docker create --name tkt-restore -v tkt-data:/data tkt:local
tar --owner=65532 --group=65532 -C backup/data -cf - . | docker cp - tkt-restore:/data
docker rm tkt-restore
```

Inicialo otra vez con las mismas opciones que tenía; el nombre de volumen, el puerto y las opciones
salen de [Despliegue](deployment.md#ejecutarla). Solo cambió el contenido.

### Verificar la restauración

```console
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
```

Las personas y los tickets del backup están ahí al iniciar sesión. Qué significa la salida de
salud está en [Despliegue](deployment.md#comprobar-que-corre).

### Permisos

Un bind mount necesita un directorio del host legible y escribible por el UID `65532`;
`chown -R 65532:65532` lo fija. Ejecutar el servidor como otro usuario no está soportado.

## Actualizar

Las imágenes salen de las etiquetas de lanzamiento descritas en
[Despliegue](deployment.md#la-imagen-de-contenedor); esta guía no supone ningún número de versión.

1. **Backupear primero.** Ejecutá el procedimiento de backup de tu instalación. Una actualización
   puede aplicar migraciones de la base de datos, y el backup es el camino de vuelta.
2. **Obtener la imagen nueva.** Para un lanzamiento publicado, fijá la etiqueta y recreá el
   contenedor:

   ```console
   TKT_TAG=<release-tag> docker compose pull
   TKT_TAG=<release-tag> docker compose up -d
   ```

   Reemplazá `<release-tag>` por la etiqueta del lanzamiento que instalás.

   Para un checkout, traé el código nuevo y volvé a construir:

   ```console
   git fetch
   git checkout <tag-or-commit>
   docker compose up -d --build
   ```

3. **Mirar el arranque.** El servidor aplica las migraciones que todavía no vio, cada una en su
   propia transacción. Una migración que falla detiene el arranque en lugar de servir una base a
   medio migrar; revisá `docker compose logs app` y restaurá el backup si falla.
4. **Verificar.** Un ticket y las páginas de configuración se muestran bien después de iniciar
   sesión; para la salida de salud, ver [Despliegue](deployment.md#comprobar-que-corre).

### Volver atrás

No hay migraciones inversas, y una imagen vieja no puede correr contra una base que una versión más
nueva ya migró.

Para volver, conservá la etiqueta que estabas usando, restaurá el backup tomado antes de la
actualización y ejecutá la imagen anterior sobre los datos restaurados.
