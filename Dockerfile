FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/agent-remote-node ./cmd/agent-remote-node

FROM alpine:3.21

COPY --from=build /out/agent-remote-node /usr/local/bin/agent-remote-node
ENTRYPOINT ["agent-remote-node"]

