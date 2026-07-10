FROM golang:1.26.5-bookworm

# hadolint ignore=DL3027
RUN apt-get update \
    && apt install apt-transport-https build-essential curl gnupg2 jq lintian python3 rsync rubygems-integration ruby-dev ruby -qy \
    && git clone https://github.com/bats-core/bats-core.git /tmp/bats-core \
    && /tmp/bats-core/install.sh /usr/local \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# hadolint ignore=DL3028
RUN gem install --quiet rake fpm package_cloud

WORKDIR /src

RUN curl -fsSLO https://download.docker.com/linux/static/stable/x86_64/docker-28.0.4.tgz \
    && tar --strip-components=1 -xvzf docker-28.0.4.tgz -C /usr/local/bin \
    && rm docker-28.0.4.tgz

RUN mkdir -p /usr/local/lib/docker/cli-plugins \
    && curl -fsSL "https://github.com/docker/compose/releases/download/v2.35.1/docker-compose-linux-x86_64" \
       -o /usr/local/lib/docker/cli-plugins/docker-compose \
    && chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
