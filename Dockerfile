# node:22-bookworm-slim は Docker 公式イメージで、ccbox update (--pull) により
# セキュリティパッチを取り込む運用のため、あえてダイジェスト固定しない。
# 固定するとベースイメージのパッチ適用が止まるデメリットの方が大きい。
FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ripgrep ca-certificates curl procps python3 \
    && rm -rf /var/lib/apt/lists/*

# uv は apt に存在しないため、公式配布イメージからスタティックバイナリをコピーする。
# 供給網対策としてダイジェストで固定する。更新時はタグとダイジェストを揃えて上げること。
COPY --from=ghcr.io/astral-sh/uv:0.9@sha256:538e0b39736e7feae937a65983e49d2ab75e1559d35041f9878b7b7e51de91e4 /uv /uvx /usr/local/bin/

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
