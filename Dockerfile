FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ripgrep ca-certificates curl procps \
    && rm -rf /var/lib/apt/lists/*

# npm インストールは非推奨のため、公式の署名付き apt リポジトリを使用する
# https://code.claude.com/docs/ja/setup#install-with-linux-package-managers
RUN install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://downloads.claude.ai/keys/claude-code.asc \
        -o /etc/apt/keyrings/claude-code.asc \
    && apt-get update \
    && apt-get install -y --no-install-recommends gnupg \
    && gpg --show-keys --with-colons /etc/apt/keyrings/claude-code.asc \
        | grep -q '^fpr:::::::::31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE:$' \
    && apt-get purge -y --auto-remove gnupg \
    && echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/latest latest main" \
        > /etc/apt/sources.list.d/claude-code.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends claude-code \
    && rm -rf /var/lib/apt/lists/*

# bind mount したホストのファイルを読み書きできるよう、ホストの UID/GID に合わせる
ARG CCBOX_UID=1000
ARG CCBOX_GID=1000
RUN userdel -r node 2>/dev/null || true \
    && (groupadd -g ${CCBOX_GID} ccbox 2>/dev/null || true) \
    && useradd -m -u ${CCBOX_UID} -g ${CCBOX_GID} -s /bin/bash ccbox

USER ccbox
WORKDIR /home/ccbox
ENV HOME=/home/ccbox

ENTRYPOINT []
CMD ["claude"]
