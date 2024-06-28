# Use the official Python image as a base
FROM python:3.10-slim

# Set the working directory
WORKDIR /usr/local/admin

# need this fixed since WHMCS does not allow custom port to be defined..
EXPOSE 2087

RUN apt-get update && apt-get install -y \
    procps \
    coreutils \
    sudo \
    wget \
    curl \
    default-mysql-client \
    sqlite3 \
    zip \
    unzip \
    geoip-bin \
    perl \
    iptables \
    libwww-perl \
    liblwp-protocol-https-perl \
    libgd-graph-perl \
    libtie-ixhash-perl \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -ms /bin/bash openadmin \
    && adduser openadmin sudo \
    && echo 'openadmin ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# here will do wget or git clone - to avoid rebuilding image
COPY . .

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt


# Make all bash scripts in this directory executable for root only
RUN chown root:root /usr/local/admin/scripts/*
RUN chmod +x -R /usr/local/admin/scripts/*

# Add opencli binary to the PATH
RUN cp /usr/local/admin/scripts/opencli /usr/local/bin/opencli
RUN chmod +x /usr/local/bin/opencli

# Set autocomplete for all available opencli commands
RUN echo "# opencli aliases\n\
ALIASES_FILE=\"/usr/local/admin/scripts/aliases.txt\"\n\
generate_autocomplete() {\n\
    awk '{print \$NF}' \"\$ALIASES_FILE\"\n\
}\n\
complete -W \"\$(generate_autocomplete)\" opencli" >> /root/.bashrc

# Gunicorn + redirect stderr to stdout
CMD ["gunicorn", "-c", "/etc/openpanel/openadmin/service/service.config.py", "app:app", "--log-file", "-"]
