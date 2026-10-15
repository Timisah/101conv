# Deploying with Portainer

The repository includes a Dockerfile and a Compose stack. Portainer can clone the Git repository and build the image itself; you do not need to build and push an image registry first.

## Before opening Portainer

The current checkout changes must be committed and pushed to a Git remote before Portainer can see them. Portainer deploys a remote Git ref, not files that only exist in this workspace. Use the repository's `main` branch after the container files are present in that branch.

## Create the stack

In Portainer, open **Stacks → Add stack → Repository** and enter:

- Repository URL: `https://github.com/Timisah/101conv`
- Repository reference: `refs/heads/main`
- Compose path: `compose.yaml`
- Authentication: configure repository credentials or a deploy key if the repository is private.

Leave the `PORT` stack variable unset to run and publish the app on port **5656**. To use another port, add a stack environment variable such as `PORT=8080`; it changes both the app's listening port and the published host port. Then deploy the stack. Portainer builds from the repository's Dockerfile and starts the service.

Open `http://<docker-host-address>:5656/`, or use the host port you selected. The H2 database, uploads, user settings, converter configuration, and job files are stored in the named `101conv-data` volume and survive container replacement.

The `PORT` variable controls both sides of the port mapping. For local Docker Compose, the same stack can be started with `docker compose up -d --build`; set a custom port with `PORT=8080 docker compose up -d --build`.

## Current deployment limits

This first stack runs the app and job worker in one container and uses file-backed H2. It is suitable for a single-node test deployment. It does not yet provide PostgreSQL, separate Core/Worker services, automated backups, volume quotas, or an OS-level converter sandbox. Back up the named volume before upgrades or host maintenance.

The image does not bundle converter executables or third-party converter manifests. The web app starts with an empty converter catalog; configure an approved converter in the persistent data volume before expecting conversions to run.
