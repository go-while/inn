# INN built from this source tree, for tests and development.
#
# Build from the top of the tree:
#
#     docker build -f contrib/docker/Dockerfile -t inn .
#     docker run --rm -p 1119:119 -e INN_GROUPS="local.test,local.misc" inn
#
# The defaults let anybody read, post and feed: see contrib/docker/README.md
# before exposing the port beyond the local machine.

ARG DEBIAN=debian:stable-slim

FROM ${DEBIAN} AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        autoconf automake bison ca-certificates flex gcc libtool make \
        libdb5.3-dev libperl-dev libsqlite3-dev libssl-dev zlib1g-dev perl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN ./autogen \
    && ./configure --prefix=/usr/local/news \
        --with-news-user=news --with-news-group=news \
        --with-sendmail=/usr/sbin/sendmail \
        --with-perl --with-openssl --with-sqlite3 \
    && make -j"$(nproc)" \
    && make install
# The runtime image installs the packages of the shared libraries INN links to.
RUN for f in /usr/local/news/bin/* /usr/local/news/bin/*/* /usr/local/news/lib/*.so*; do \
        ldd "$f" 2>/dev/null; \
    done | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | sort -u \
    | while read -r lib; do \
        dpkg -S "$(readlink -f "$lib")" 2>/dev/null || dpkg -S "$lib" 2>/dev/null \
            || dpkg -S "/usr$lib" 2>/dev/null || true; \
    done | cut -d: -f1 | sort -u > /runtime-packages \
    && cat /runtime-packages

FROM ${DEBIAN}
COPY --from=build /runtime-packages /tmp/runtime-packages
RUN apt-get update \
    && apt-get install -y --no-install-recommends perl libnet-perl $(cat /tmp/runtime-packages) \
    && rm -rf /var/lib/apt/lists/* /tmp/runtime-packages
COPY --from=build /usr/local/news /usr/local/news
COPY contrib/docker/entrypoint.sh /usr/local/bin/inn-entrypoint
ENV PATH=/usr/local/news/bin:$PATH
EXPOSE 119
VOLUME ["/usr/local/news/spool", "/usr/local/news/db", "/usr/local/news/log"]
ENTRYPOINT ["/usr/local/bin/inn-entrypoint"]
