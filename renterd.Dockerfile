# build sia
FROM golang:alpine AS build

WORKDIR /build

COPY . .

# need to run git status first to fix GIT_DIRTY detection in makefile
RUN apk update \
	&& apk add --no-cache build-base git make ca-certificates \
	&& update-ca-certificates \
	&& git status > /dev/null \
	&& make build-renter

FROM alpine:latest

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /build/bin /

# web port
EXPOSE 9980/tcp
# gateway port
EXPOSE 9981/tcp

VOLUME [ "/data" ]

ENTRYPOINT [ "/renterd", "--dir", "/data", "--http", ":9980" ]