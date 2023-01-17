# build sia
FROM golang:alpine AS build

WORKDIR /app

COPY . .

# need to run git status first to fix GIT_DIRTY detection in makefile
RUN apk update \
	&& apk add --no-cache build-base git make ca-certificates \
	&& update-ca-certificates \
	&& git status > /dev/null \
	&& make static

FROM alpine:latest

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /app/release /

# gateway port
EXPOSE 9981/tcp
# host RHP2 port
EXPOSE 9982/tcp
# host RHP3 port
EXPOSE 9983/tcp

# SIA_WALLET_PASSWORD is used to automatically unlock the wallet
ENV SIA_WALLET_PASSWORD=
# SIA_API_PASSWORD sets the password used for API authentication
ENV SIA_API_PASSWORD=

VOLUME [ "/sia-data" ]

ENTRYPOINT [ "/siad", "--disable-api-security", "-d", "/sia-data", "--api-addr", ":9980" ]