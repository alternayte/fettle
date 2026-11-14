# fettle-computer: the image of every computer (SDD §10.5). Debian slim, non-root user
# fettle, Chromium, Xvfb, x11vnc, noVNC, git, ripgrep, curl, jq, Python 3 with uv,
# Node 22, pdftotext, pandoc and fettle-agentd.

FROM golang:1.26-bookworm AS agentd
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY agentd ./agentd
COPY cmd/fettle-agentd ./cmd/fettle-agentd
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/fettle-agentd ./cmd/fettle-agentd

FROM debian:bookworm-slim
ARG NODE_MAJOR=22
ARG NOVNC_VERSION=1.5.0
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg jq git ripgrep python3 python3-venv \
      chromium fonts-liberation xvfb x11vnc nftables iproute2 \
      poppler-utils pandoc procps tini \
 && mkdir -p /etc/apt/keyrings \
 && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
 && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" > /etc/apt/sources.list.d/nodesource.list \
 && apt-get update && apt-get install -y --no-install-recommends nodejs \
 && curl -fsSL https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz | tar -xz -C /opt \
 && mv /opt/noVNC-${NOVNC_VERSION} /opt/novnc \
 && curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin INSTALLER_NO_MODIFY_PATH=1 sh \
 && apt-get purge -y gnupg && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/* /usr/share/doc /usr/share/man \
 && useradd --create-home --home-dir /home/fettle --shell /bin/bash --uid 1000 fettle \
 && rm -f /usr/bin/sudo /usr/bin/su
COPY --from=agentd /out/fettle-agentd /usr/local/bin/fettle-agentd
ENV FETTLE_AGENTD_HOME=/home/fettle FETTLE_AGENTD_USER=fettle CHROME_BIN=/usr/bin/chromium
EXPOSE 7421
# agentd starts as root only to apply the network rules and prepare folders, then drops to fettle.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/fettle-agentd"]
