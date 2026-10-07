# Installing on a NAS

Switch Library Manager Web runs as one container on any NAS with Docker. Below are the steps for the most common
systems; [Unraid](../../README.md#unraid) has its own template in Community Applications.

On every system you need:

- **A folder for the app's data** (settings, caches, covers). Put **your own** `prod.keys` there, dumped from your
  console; no keys are included or downloaded.
- **The folder of your games** (NSP, NSZ, XCI, XCZ), readable and writable so the app can organize and compress them.
- **PUID and PGID**: the user and group that own your games folder. The app runs as that user, never as root.

After the installation, open `http://<address of the NAS>:3000` and set your folders in **Settings**. Create an
administrator in **Users** before the app can be reached from outside your network.

- [Synology](#synology)
- [TrueNAS SCALE](#truenas-scale)
- [CasaOS and ZimaOS](#casaos-and-zimaos)
- [Portainer](#portainer)
- [Updating](#updating)

## Synology

DSM 7.2 or newer, with **Container Manager** installed from the Package Center.

1. In **File Station**, create the folder `docker/switch-library-manager-web` (for the app) and, if you do not have
   one yet, a shared folder for your games, e.g. `Switch`.
2. Find your user and group IDs: over SSH run `id your-user`. The first user of a Synology is usually `1026`, the
   `users` group `100`.
3. In **Container Manager > Project > Create**:
   - **Project name**: `switch-library-manager-web`
   - **Path**: `/volume1/docker/switch-library-manager-web`
   - **Source**: *Create docker-compose.yml*, and paste
     [templates/synology/docker-compose.yml](../../templates/synology/docker-compose.yml).
4. Change `/volume1/Switch` to the folder of your games, and `PUID`/`PGID` if yours are different. Click **Next** and
   **Done**: the image is downloaded and the app starts.

## TrueNAS SCALE

TrueNAS SCALE 24.10 (Electric Eel) or newer, which runs apps with Docker.

1. Create the datasets: one for the app (e.g. `tank/apps/switch-library-manager-web`) and the one of your games
   (e.g. `tank/switch`).
2. Give read and write access to both to the user that will run the app. `568` is the `apps` user of TrueNAS; you can
   also use your own user and group.
3. In **Apps > Discover Apps**, open the **⋮** menu and choose **Install via YAML**. Name it
   `switch-library-manager-web` and paste [templates/truenas/docker-compose.yml](../../templates/truenas/docker-compose.yml).
4. Change the two paths under `volumes` to your datasets and click **Save**.

## CasaOS and ZimaOS

1. In the **App Store**, click **Custom Install** (the **+** or the icon at the top right) and then **Import**.
2. Paste [templates/casaos/docker-compose.yml](../../templates/casaos/docker-compose.yml) and click **Submit**: the
   app gets its name, icon and descriptions.
3. Check the two folders: `/DATA/AppData/switch-library-manager-web` for the app and `/DATA/Gallery/Switch` for your
   games (change it to where your games are). CasaOS fills in PUID, PGID and the time zone.
4. Click **Install** and open the app from the dashboard.

## Portainer

**As a template** (it appears in *App Templates* with its icon and fields):

1. In **Settings > App Templates**, set the URL to
   `https://raw.githubusercontent.com/DeLFuS77/switch-library-manager-web/master/templates/portainer.json` and save.
2. In **App Templates**, choose **Switch Library Manager Web**, set the folder of your games and PUID/PGID, and
   deploy it.

**As a stack**: in **Stacks > Add stack**, paste [docker-compose.yml](../../docker-compose.yml), set the folders and
deploy it.

## Updating

- **Synology**: Container Manager > Project > the project > **Action > Build** (it pulls the new image), or Image >
  **Update** when it shows that a new version is available.
- **TrueNAS SCALE**: Apps > the app > **Update** when it is offered, or Edit > Save to pull the new image.
- **CasaOS / ZimaOS**: the app's settings > **Update**, or reinstall it with the same folders.
- **Portainer**: the container > **Recreate** with *Pull latest image*, or the stack > **Pull and redeploy**.

The data folder keeps the settings, users and covers, so nothing is lost when the container is recreated.
