FROM golang@sha256:0bf61ce389ea41b61ea9f2b2c8eb15824838782df0c9c302dc284f0d6792caf9
# golang:1.16.7-buster

ARG branch
ARG version

RUN useradd -ms /bin/bash builder
RUN chown -R builder:builder /home/builder
USER builder
WORKDIR /home/builder

RUN git clone https://github.com/SiaFoundation/siad /home/builder/Sia
WORKDIR /home/builder/Sia
RUN git fetch --all && git checkout $branch

RUN ./release-scripts/release.sh $version
