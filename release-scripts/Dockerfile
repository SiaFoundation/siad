FROM golang@sha256:1e9c36b3fd7d7f9ab95835fb1ed898293ec0917e44c7e7d2766b4a2d9aa43da6
# golang:1.14.4-buster

ARG branch
ARG version

RUN useradd -ms /bin/bash builder
USER builder
WORKDIR /home/builder

RUN git clone https://gitlab.com/NebulousLabs/Sia
WORKDIR /home/builder/Sia
RUN git fetch --all && git checkout $branch

RUN ./release-scripts/release.sh $version
