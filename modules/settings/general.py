################################################################################
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
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url


@app.route('/settings/general/update_now', methods=['POST'])
@login_required_route
def update_now():
    try:
        # Start the update process in the background
        subprocess.Popen(['opencli', 'update', '--force'], start_new_session=True)
        return jsonify({"message": "Update process started successfully."}), 200
    except Exception as e:
        return jsonify({"error": "Failed to start the update process.", "details": str(e)}), 500



@app.route('/settings/general', methods=['GET', 'POST'])
@login_required_route
def admin_general_settings():

    # Load the configuration data
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)

    if request.method == 'POST':
        # Get values from the form fields
        domain_name_is_set = request.form.get('form-domain-name-is-set') == 'domain_name'
        ip_address_is_set = request.form.get('form-domain-name-not-set-use-shared-ip') == 'ip_address'

        force_domain = request.form.get('force_domain')
        ssl_enabled = 'ssl' in request.form

        admin_port_value = request.form.get('2087_port')
        openpanel_port_value = request.form.get('2083_port')
        openpanel_proxy = request.form.get('openpanel_proxy')

        admin_port = int(admin_port_value) if admin_port_value else 2087
        openpanel_port = int(openpanel_port_value) if openpanel_port_value else 2083

        autoupdate_enabled = 'autoupdate' in request.form
        autopatch_enabled = 'autopatch' in request.form

        force_domain_current_value = config_data.get('DEFAULT', {}).get('force_domain', '')
        openpanel_port_current_value = config_data.get('DEFAULT', {}).get('port', '2083')
        openpanel_proxy_current_value = config_data.get('DEFAULT', {}).get('openpanel_proxy', 'openpanel')
        ssl_current_value = config_data.get('DEFAULT', {}).get('ssl', 'yes')
        autoupdate_current_value = config_data.get('PANEL', {}).get('autoupdate', 'on')
        autopatch_current_value = config_data.get('PANEL', {}).get('autopatch', 'on')

        success_messages = []
        error_messages = []
        openpanel_service_restart_is_needed = False
        adminpanel_service_restart_is_needed = False



        if int(openpanel_port) != int(openpanel_port_current_value):
            command = f"opencli config update port '{openpanel_port}'"
            success_message = f"OpenPanel port changed from {openpanel_port_current_value} to {openpanel_port}. Do not forget to open port {openpanel_port} on the firewall!"
            error_message = f"Error: OpenPanel port could not be changed."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated port to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if ssl_enabled and ssl_current_value == 'yes':
            pass
        elif ssl_enabled and ssl_current_value != 'yes' and domain_name_is_set:
            command = "opencli config update ssl yes"
            success_message = "SSL is now enabled for the panels."
            error_message = f"Error: SSL could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated ssl to yes" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                    adminpanel_service_restart_is_needed = True                    
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif ssl_enabled and ssl_current_value != 'yes' and ip_address_is_set and not force_domain:
            error_messages.append("SSL can not be enabled without a domain name.")
        elif not ssl_enabled and ssl_current_value == 'yes' and ip_address_is_set and not domain_name_is_set:
            command = "opencli config update ssl no"
            success_message = "SSL is now disabled for the panels."
            error_message = f"Error: SSL could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated ssl to no" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                    adminpanel_service_restart_is_needed = True  
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif not ssl_enabled and ssl_current_value != 'yes' and ip_address_is_set and not force_domain:
            pass
            

        # use domain name for the panels!
        if openpanel_proxy:
            if openpanel_proxy == openpanel_proxy_current_value:
                pass
            else:
                openpanel_proxy
                command = f"opencli config update openpanel_proxy '{openpanel_proxy}'"
                success_message = f"{openpanel_proxy} is now set for custom path for all user domains to access openpanel."
                error_message = f"Error: {openpanel_proxy} could not be set as openpanel_proxy."
                try:
                    result = subprocess.check_output(command, shell=True, text=True)
                    if "Updated openpanel_proxy to" in result:
                        success_messages.append(success_message)
                        pass
                    else:
                        error_messages.append(error_message)
                except subprocess.CalledProcessError as e:
                    error_messages.append(f"Error executing command: '{command}': {e.output}")


        # use domain name for the panels!
        if domain_name_is_set:
            server_hostname = socket.gethostname() or  subprocess.check_output(["hostname"]).decode("utf-8").strip()
            # novi domen mora biti hostname servera i editujemo samo ako nije vec u fajlu!
            if force_domain == server_hostname and not force_domain_current_value == force_domain:
                command = "opencli ssl-hostname"
                success_message = f"{force_domain} is set as the domain name for both panels."
                error_message = f"Error: {force_domain} could not be set as the domain name."
                try:
                    result = subprocess.check_output(command, shell=True, text=True)
                    if "success" in result:
                        success_messages.append(success_message)
                        pass
                    else:
                        error_messages.append(error_message)
                except subprocess.CalledProcessError as e:
                    error_messages.append(f"Error executing command: '{command}': {e.output}")

            elif force_domain == server_hostname and force_domain_current_value == force_domain:
                pass

            else:
                error_message = f"Error: {force_domain} needs to be first set as the server hostname in order to be used as the domain name for the panels."
                error_messages.append(error_message)

        elif ip_address_is_set and force_domain_current_value:
            command = "opencli config update force_domain ''"
            success_message = "Panels are accessible on IP address"
            error_message = f"Error: IP address could not be set for accessing panels."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated force_domain to" in result:
                    command = "opencli config update ssl no"
                    try:
                        result = subprocess.check_output(command, shell=True, text=True)
                        if "Updated ssl to no" in result:
                            pass
                    except subprocess.CalledProcessError as e:
                        error_messages.append(f"Error executing command: '{command}': {e.output}")

                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                    adminpanel_service_restart_is_needed = True 
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}") 


        # set autoupdate or autopatch settings
        if autoupdate_enabled and autoupdate_current_value == "off":
            command = f"opencli config update autoupdate on"
            success_message = "Autoupdates enabled"
            error_message = "Error enabling autoupdates"
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated autoupdate to on" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        elif not autoupdate_enabled and autoupdate_current_value == "on":
            command = f"opencli config update autoupdate off"
            success_message = "Autoupdates disabled"
            error_message = "Error disabling autoupdates"
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated autoupdate to off" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        else:
            pass

        
        if autopatch_enabled and autopatch_current_value == "off":
            command = f"opencli config update autopatch on"
            success_message = "Autopatches enabled"
            error_message = "Error enabling autopatches"
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated autopatch to on" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        elif not autopatch_enabled and autopatch_current_value == "on":
            command = f"opencli config update autopatch off"
            success_message = "Autopatches disabled"
            error_message = "Error disabling autopatches"
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated autopatch to off" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        # Return accumulated messages at the end
        response_data = {}
        if success_messages:
            response_data["success_messages"] = success_messages
        if error_messages:
            response_data["error_messages"] = error_messages


        # restart services only when needed!
        if openpanel_service_restart_is_needed:
            openpanel_service_restart = "service panel restart"
            try:
                subprocess.check_output(openpanel_service_restart, shell=True, text=True)
            except subprocess.CalledProcessError as e:
                response_data["openpanel_service"] = f"Error restarting OpenPanel service: '{openpanel_service_restart}': {e.output}"

        if adminpanel_service_restart_is_needed:
            adminpanel_service_restart = "service admin restart"
            try:
                subprocess.check_output(adminpanel_service_restart, shell=True, text=True)
            except subprocess.CalledProcessError as e:
                response_data["openadmin_service"] = f"Error restarting AdminPanel service: '{adminpanel_service_restart}': {e.output}"


        if response_data:
            return jsonify(response_data)

    else:
        current_route = request.path
        server_hostname = socket.gethostname() or  subprocess.check_output(["hostname"]).decode("utf-8").strip()
        #legacy, will be repalced with openadmin > tempaltes
        howto_custom_content_for_users = '/etc/openpanel/openpanel/conf/knowledge_base_articles.json'
        try:
            with open(howto_custom_content_for_users, 'r') as file:
                howto_content_current_value = file.read()
                # Check if the file is empty
                if not howto_content_current_value:
                    howto_content_current_value = None
        except FileNotFoundError:
            howto_content_current_value = "File not found"
        except Exception as e:
            howto_content_current_value = f"Error: {str(e)}"


        return render_template('general_settings.html', title='General Settings', current_route=current_route, app=app, config_data=config_data, server_hostname=server_hostname, howto_content_current_value=howto_content_current_value)

