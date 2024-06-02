FROM golang:1.22-alpine AS build

LABEL description="Rota IP Rotator"
LABEL repository="https://github.com/alpkeskin/rota"
LABEL maintainer="alpkeskin"

WORKDIR /app
COPY ./go.mod .
RUN go mod download

COPY . .
RUN go build -ldflags "-s -w" \
	-o ./bin/rota ./cmd/rota 

FROM alpine:latest

COPY --from=build /app/bin/rota /bin/rota
ENV HOME /
ENTRYPOINT ["/bin/rota"]
