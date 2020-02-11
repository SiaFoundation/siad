FROM golang@sha256:f6cefbdd25f9a66ec7dcef1ee5deb417882b9db9629a724af8a332fe54e3f7b3
ARG branch
ARG version

RUN useradd -ms /bin/bash builder
USER builder
WORKDIR /home/builder

RUN git clone https://gitlab.com/NebulousLabs/Sia
WORKDIR /home/builder/Sia
RUN git fetch --all && git checkout $branch

RUN ./release-scripts/release.sh $version
