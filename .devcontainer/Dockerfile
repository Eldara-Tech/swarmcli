FROM mcr.microsoft.com/devcontainers/go:dev-1.26

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gnupg \
        lsb-release \
        ccze \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian \
       $(. /etc/os-release && echo \"$VERSION_CODENAME\") stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        docker-ce-cli \
        docker-buildx-plugin \
        docker-compose-plugin \
        telnet \
    && rm -rf /var/lib/apt/lists/*

ARG USERNAME=vscode
RUN groupadd -f docker && usermod -aG docker ${USERNAME}

ENV GOTOOLCHAIN=auto

ENV GONOSUMDB=* \
    GONOPROXY=* \
    GOPROXY=https://proxy.golang.org,direct

RUN echo 'deb [trusted=yes] https://repo.goreleaser.com/apt/ /' > /etc/apt/sources.list.d/goreleaser.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends goreleaser \
    && rm -rf /var/lib/apt/lists/*