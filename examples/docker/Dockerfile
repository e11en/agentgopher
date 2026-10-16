FROM node:22-bookworm
ARG AGENT_UID=1000
ARG AGENT_GID=1000
RUN apt-get update && apt-get install -y --no-install-recommends git sudo && rm -rf /var/lib/apt/lists/* \
  && groupmod -o -g $AGENT_GID node && usermod -o -u $AGENT_UID -g $AGENT_GID -d /home/agent -m -l agent node \
  && echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent \
  && npm install -g @anthropic-ai/claude-code
USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /home/agent/workspace
ENTRYPOINT ["sleep", "infinity"]
