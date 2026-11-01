# Installing on a NAS

Switch Library Manager Web runs as one container on any NAS with Docker. Below are the steps for Synology and
Portainer; [Unraid](https://github.com/DeLFuS77/switch-library-manager-web/wiki/Installation#unraid) has its own template in Community Applications, and on any other system
with Docker Compose the [docker-compose.yml](../../docker-compose.yml) of the repository works as it is.

On every system you need:

- **A folder for the app's data** (settings, caches, covers). Put **your own** `prod.keys` there, dumped from your
  console; no keys are included or downloaded.
- **The folder of your games** (NSP, NSZ, XCI, XCZ), readable and writable so the app can organize and compress them.
  Games in a second folder, such as another disk, can be mounted at `/mnt/roms2` (the compose files have the line ready)
  and added to **Folders to scan** in **Settings › Library**.
- **PUID and PGID**: the user and group that own your games folder. The app runs as that user, never as root.

After the installation, open `http://<address of the NAS>:3000` and set your folders in **Settings**. Create an
administrator in **Users** before the app can be reached from outside your network. The first one needs the setup code
that the app writes in the log of the container when it starts without users: look for `SETUP CODE` (Synology:
Container Manager > Container > the app > Details > Log; Portainer: Containers > the app > Logs). It works once and
changes every time the app starts.

- [Synology](#synology)
- [Portainer](#portainer)
- [Updating](#updating)
- [Signing in through Authelia, Authentik or Cloudflare Access](#signing-in-through-authelia-authentik-or-cloudflare-access)

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
4. Change `/volume1/Switch` to the folder of your games, and `PUID`/`PGID` if yours are different. For games in a
   second folder, remove the `#` of the `/mnt/roms2` line and set its folder. Click **Next** and **Done**: the image
   is downloaded and the app starts.

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
- **Portainer**: the container > **Recreate** with *Pull latest image*, or the stack > **Pull and redeploy**.

The data folder keeps the settings, users and covers, so nothing is lost when the container is recreated.

## Signing in through Authelia, Authentik or Cloudflare Access

If a sign-in portal already protects the app, the people it lets in can open the app as their user, without its
password. It is off until you turn it on in **Settings › Sign-in portal**, or with the variables below; a variable
wins over its option, which Settings then shows locked. Passwords and API keys keep working, and while the app has no
user the portal does nothing: the first administrator still needs the setup code.

Each person is matched with a user of the app by name or by email: set the email of each user in **Users › Edit**. A
person without a user gets the login page, unless the app is set to make them a guest or a player (never an
administrator). The members of a group of the portal (`SLM_AUTH_PROXY_ADMIN_GROUP`, e.g. `slm-admins`) are
administrators while they are in it.

**Authelia or Authentik**, with the forward auth of nginx, Traefik or Caddy: the proxy names the person in a header,
and the app believes it only from the addresses of `SLM_TRUSTED_PROXIES`, never from those of Cloudflare. From
anywhere else the header is ignored and noted in **Activity**.

```yaml
    environment:
      - SLM_TRUSTED_PROXIES=172.18.0.10                 # the address of your proxy
      - SLM_AUTH_PROXY_HEADER=Remote-User               # Authentik: X-authentik-username
      - SLM_AUTH_PROXY_EMAIL_HEADER=Remote-Email        # Authentik: X-authentik-email
      - SLM_AUTH_PROXY_GROUPS_HEADER=Remote-Groups      # Authentik: X-authentik-groups
      - SLM_AUTH_PROXY_LOGOUT_URL=https://auth.example.com/logout
```

With Authentik the logout address is `/outpost.goauthentik.io/sign_out`, on the address of the app. The proxy must
set these headers itself on every request, as the forward auth of Authelia and Authentik does. A tunnel that only
passes requests on, like cloudflared, does not: with a Cloudflare Tunnel, use Cloudflare Access instead.

**Cloudflare Access**: Cloudflare signs every request it lets through. The app checks the signature with the public
keys of your team, downloaded over https, and knows each person by their email.

```yaml
    environment:
      - SLM_CF_ACCESS_TEAM=myteam                       # myteam.cloudflareaccess.com
      - SLM_CF_ACCESS_AUD=4714c1358e65fe4b408ad6d432a5f878f08194bdb4752441fd56faefa9b2b6f2
```

The AUD tag is in **Zero Trust › Access › Applications ›** your application **› Overview**. Log out goes to the logout
page of Cloudflare Access.

| Variable | Meaning |
|---|---|
| `SLM_AUTH_PROXY_HEADER` | The header of the user name; set, the header is believed |
| `SLM_AUTH_PROXY_EMAIL_HEADER`, `SLM_AUTH_PROXY_GROUPS_HEADER` | The headers of the email and the groups (`Remote-Email`, `Remote-Groups`) |
| `SLM_CF_ACCESS_TEAM`, `SLM_CF_ACCESS_AUD` | The team of Cloudflare Zero Trust and the AUD tag of the application; set, Cloudflare Access is believed |
| `SLM_AUTH_PROXY_MATCH` | `name` or `email` to match people only by name or only by email; empty, by either |
| `SLM_AUTH_PROXY_NEW_USERS` | `guest` or `player` to make a user for a person without one; empty, none is made |
| `SLM_AUTH_PROXY_ADMIN_GROUP` | The group of the portal whose members are administrators |
| `SLM_AUTH_PROXY_LOGOUT_URL` | Where Log out sends the people signed in by the portal |
