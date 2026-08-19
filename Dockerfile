FROM golang:1.26.6-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/agent-remote-node ./cmd/agent-remote-node
RUN go build -o /out/agent-remote-attach ./cmd/agent-remote-attach
RUN go build -o /out/agent-remote-runtime ./cmd/agent-remote-runtime

FROM alpine:3.21

COPY --from=build /out/agent-remote-node /usr/local/bin/agent-remote-node
COPY --from=build /out/agent-remote-attach /usr/local/bin/agent-remote-attach
COPY --from=build /out/agent-remote-runtime /usr/local/bin/agent-remote-runtime
ENTRYPOINT ["agent-remote-node"]
