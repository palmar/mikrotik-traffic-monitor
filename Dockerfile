FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /traffic-monitor .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /traffic-monitor /traffic-monitor
EXPOSE 8080
ENTRYPOINT ["/traffic-monitor"]
