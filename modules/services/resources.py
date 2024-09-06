################################################################################
# Author: Stefan Pejcic
# Created: 01.07.2024
# Last Modified: 01.07.2024
# Company: OPENPANEL
# Copyright (c) openpanel.co
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# 
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# 
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################

# import python modules
from flask import Flask, request, render_template
import yaml
import psutil


# import our modules
from app import app, login_required_route



@app.route('/services/resources', methods=['GET', 'POST'])
@login_required_route
def services_resources():
    # Read the docker-compose.yml file
    with open('/root/docker-compose.yml', 'r') as file:
        docker_compose = yaml.safe_load(file)

    # Function to safely extract resource limits
    def get_limits(service_name):
        service = docker_compose['services'].get(service_name)
        if not service:
            return None
        deploy = service.get('deploy')
        if not deploy:
            return None
        resources = deploy.get('resources')
        if not resources:
            return None
        return resources.get('limits')

    # Extract resource limits for services
    openpanel_limits = {
        'mem_limit': docker_compose['services'].get('openpanel', {}).get('mem_limit'),
        'cpus': docker_compose['services'].get('openpanel', {}).get('cpus')
    }

    # Extract resource limits for services
    openpanel_mysql_limits = {
        'mem_limit': docker_compose['services'].get('openpanel_mysql', {}).get('mem_limit'),
        'cpus': docker_compose['services'].get('openpanel_mysql', {}).get('cpus')
    }

    dns_limits = {
        'mem_limit': docker_compose['services'].get('bind9', {}).get('mem_limit'),
        'cpus': docker_compose['services'].get('bind9', {}).get('cpus')
    }
    certbot_limits = {
        'mem_limit': docker_compose['services'].get('certbot', {}).get('mem_limit'),
        'cpus': docker_compose['services'].get('certbot', {}).get('cpus')
    }
    nginx_limits = {
        'mem_limit': docker_compose['services'].get('nginx', {}).get('mem_limit'),
        'cpus': docker_compose['services'].get('nginx', {}).get('cpus')
    }    

    mailserver_limits = get_limits('openadmin_mailserver')
    roundcube_limits = get_limits('openadmin_roundcube')

    # Read the current swap setting
    swap_total = None
    with open('/proc/meminfo', 'r') as file:
        for line in file:
            if line.startswith('SwapTotal'):
                swap_total = line.split()[1]  # Get the value in kB
                swap_total = round(int(swap_total) / (1024 * 1024), 2)  # Convert to GB and round

    # Get total RAM and CPU information
    mem_total = psutil.virtual_memory().total / (1024 * 1024 * 1024)  # in GB
    mem_total = round(mem_total, 2)  # Round to two decimal places
    cpu_count = psutil.cpu_count(logical=False)  # physical cores

    return render_template('services/resources.html',
                           openpanel_mysql_limits=openpanel_mysql_limits,
                           openpanel_limits=openpanel_limits,
                           mailserver_limits=mailserver_limits,
                           roundcube_limits=roundcube_limits,
                           nginx_limits=nginx_limits,
                           dns_limits=dns_limits,
                           certbot_limits=certbot_limits,
                           swap_total=swap_total,
                           mem_total=mem_total,
                           cpu_count=cpu_count)

