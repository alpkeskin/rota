FROM golang:1.23.4-alpine AS build

LABEL description="Rota Proxy"
LABEL repository="https://github.com/alpkeskin/rota"
LABEL maintainer="alpkeskin"

WORKDIR /app

COPY ./go.mod .

RUN go mod download

COPY . .

RUN go build -ldflags "-s -w" \
	-o ./bin/rota ./cmd/rota 


FROM alpine:3.21

RUN mkdir -p /var/log/rota && \
    mkdir -p /etc/rota && \
    adduser -D rota && \
    chown -R rota:rota /var/log/rota && \
    chown -R rota:rota /etc/rota

COPY --from=build /app/bin/rota /bin/rota

USER rota

WORKDIR /etc/rota

EXPOSE 8080
EXPOSE 8081

ENTRYPOINT ["/bin/rota"]
