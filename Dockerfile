FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ripgrep ca-certificates curl procps \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

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
