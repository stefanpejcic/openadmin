################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:29:51                                       *
# *                                                                       *
# *************************************************************************
# *                                                                       *
# * Email: info@openpanel.com                                             *
# * Website: https://openpanel.com                                        *
# *                                                                       *
# *************************************************************************
# *                                                                       *
# * This software is furnished under a license and may be used and copied *
# * only  in  accordance  with  the  terms  of such  license and with the *
# * inclusion of the above copyright notice.  This software  or any other *
# * copies thereof may not be provided or otherwise made available to any *
# * other person.  No title to and  ownership of the software is  hereby *
# * transferred.                                                          *
# *                                                                       *
# * You may not reverse  engineer, decompile, defeat  license  encryption *
# * mechanisms, or  disassemble this software product or software product *
# * license.  OpenPanel may terminate this license if you don't comply    *
# * with any of the terms and conditions set forth in our end user        *
# * license agreement (EULA).  In such event,  licensee  agrees to return *
# * licensor  or destroy  all copies of software  upon termination of the *
# * license.                                                              *
# *                                                                       *
# * Please see the EULA file for the full End User License Agreement.     *
# *                                                                       *
# *************************************************************************
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 09.03.2024
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




import os
import re
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, cache, is_license_valid, admin_required_route, get_openpanel_port, get_openpanel_proxy, get_openpanel_domain, get_ip_address
import docker


@app.route('/settings/general', methods=['GET', 'POST'])
@admin_required_route
@cache.memoize(timeout=30)
def admin_general_settings():

    if request.method == 'POST':
        changes = []
        
        force_domain = request.form.get('force_domain')

        if force_domain:
            domain_name_is_set = True
            ip_address_is_set = False
        else:
            ip_address_is_set = True
            domain_name_is_set = False

        # todo: validate domain!

        admin_port_value = request.form.get('2087_port')
        openpanel_port_value = request.form.get('2083_port')
        openpanel_proxy = request.form.get('openpanel_proxy')

        admin_port = int(admin_port_value) if admin_port_value else 2087
        openpanel_port = int(openpanel_port_value) if openpanel_port_value else 2083

        force_domain_current_value = get_openpanel_domain()
        if force_domain_current_value != None:
            ip_pattern = r"^(\d{1,3}\.){3}\d{1,3}$"
            if re.match(ip_pattern, force_domain_current_value):
                force_domain_current_value = None
            else:
                force_domain_current_value = force_domain_current_value

        openpanel_port_current_value = get_openpanel_port()

        if int(openpanel_port) != int(openpanel_port_current_value):
            command = f"opencli port set '{openpanel_port}' --no-restart"
            print(f"GENERAL - Executing: {command}")
            changes.append("port")
            result = subprocess.run(command, shell=True, text=True)          

        if openpanel_proxy:
            command = ["opencli", "proxy", "set", openpanel_proxy, "--no-restart"]
            print(f"GENERAL - Executing: {command}")
            changes.append("proxy")
        else:
            command = ["opencli", "proxy", "set", 'openpanel', "--no-restart"]
            print(f"GENERAL - Executing: {command}")
            changes.append("proxy")
        subprocess.run(command, text=True)

        if domain_name_is_set:
            if force_domain and force_domain_current_value != force_domain:
                command = f"opencli domain set {force_domain} --no-restart"
                print(f"GENERAL - Executing: {command}")
                changes.append("domain")
                result = subprocess.run(command, shell=True, text=True)
            elif force_domain and force_domain_current_value == force_domain:
                pass

        elif ip_address_is_set and force_domain_current_value:
            command = "opencli domain ip"
            print(f"GENERAL - Executing: {command}")
            changes.append("ip")
            result = subprocess.run(command, shell=True, text=True)


        # restart services only when needed!
        print(f"GENERAL - Writing restart needed flag for OpenPanel")
        file_path = '/root/openpanel_restart_needed'
        with open(file_path, 'w') as f:
            f.write("Restart needed") 

        print(f"GENERAL - Writing restart needed flag for OpenAdmin")
        file_path = '/root/openadmin_restart_needed'
        with open(file_path, 'w') as f:
            f.write("Restart needed") 

        if changes:
            flash("Settings updated: " + ", ".join(changes), "success")
        else:
            flash("No changes made.", "info")

    current_route = request.path
    server_hostname = socket.gethostname() or  subprocess.check_output(["hostname"]).decode("utf-8").strip()

    # invalidate caches so we display different on page reload!
    print(f"GENERAL - Invalidating cached data: proxy, domain, port, IP address..")
    cache.delete_memoized(get_openpanel_proxy)
    cache.delete_memoized(get_openpanel_domain)
    cache.delete_memoized(get_openpanel_port)
    cache.delete_memoized(get_ip_address)

    port = get_openpanel_port()
    proxy = get_openpanel_proxy()
    force_domain = get_openpanel_domain()
    public_ip = get_ip_address()

    return render_template('settings/general.html', title='General Settings', current_route=current_route, app=app, server_hostname=server_hostname, port=port, proxy=proxy, force_domain=force_domain, public_ip=public_ip)
