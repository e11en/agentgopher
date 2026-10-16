# Build the agentgopher CLI.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/agentgopher ./cmd/agentgopher

# Runner image: runs user scripts with `go run`, so it keeps the Go toolchain.
# git and gh for clone, push and PR; node for the Claude Code CLI.
FROM golang:1.26-bookworm
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git gnupg \
  && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
  && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
  && apt-get install -y --no-install-recommends gh nodejs \
  && npm install -g @anthropic-ai/claude-code \
  && rm -rf /var/lib/apt/lists/*
RUN useradd --create-home --uid 1000 agent
COPY --from=build /out/agentgopher /usr/local/bin/agentgopher
USER agent
WORKDIR /work
ENV GOPRIVATE=github.com/e11en GOFLAGS=-mod=mod GOPATH=/home/agent/go GOCACHE=/home/agent/.cache/go-build
ENTRYPOINT ["agentgopher"]
CMD ["remote", "entrypoint"]
