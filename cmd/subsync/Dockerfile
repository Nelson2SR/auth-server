FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /subsync ./cmd/subsync

FROM alpine:3
COPY --from=build /subsync /subsync
# loop the one-shot CLI on an interval
ENTRYPOINT ["sh","-c","while true; do /subsync; sleep ${SYNC_INTERVAL:-300}; done"]
