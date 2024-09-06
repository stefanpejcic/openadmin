################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 20.08.2024
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
import os
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess

# import our modules
from app import app, login_required_route 

# helper function, should be moved to modules.helpers
def load_openpanel_config(config_file_path):
    config_data = {}
    with open(config_file_path, 'r') as file:
        for line in file:
            line = line.strip()
            if line.startswith('['):
                section_title = line.strip('[]')
            elif line and '=' in line:
                key, value = line.split('=', 1)
                if section_title:
                    if section_title not in config_data:
                        config_data[section_title] = {}
                    config_data[section_title][key] = value
    return config_data

# acknowledge notification
@app.route('/mark_as_read/<int:line_number>', methods=['POST'])
@login_required_route
def mark_notification_as_read(line_number):
    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    try:
        with open(log_file, 'r') as f:
            lines = f.readlines()

        command = request.form.get('command', '')

        if command == 'mark_all_as_read':
            # Mark all notifications as READ
            lines = [line.replace('UNREAD', 'READ') for line in lines]
        elif 1 <= line_number <= len(lines):
            # Mark a specific notification as READ from the bottom
            lines[-line_number] = lines[-line_number].replace('UNREAD', 'READ')
        else:
            return abort(400, "Invalid line number")

        with open(log_file, 'w') as f:
            f.writelines(lines)

        return redirect(url_for('view_notifications'))

    except FileNotFoundError:
        return abort(400, "Log file not found")
    except Exception as e:
        return abort(500, f"Error marking notification as read: {e}")


# view notifications
@app.route('/view_notifications', methods=['GET', 'POST'])
@login_required_route
def view_notifications():
    config_file_path = '/etc/openpanel/openadmin/config/notifications.ini'
    main_config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    if request.method == 'POST':
        config_data = load_openpanel_config(config_file_path)
        # post data
        reboot = request.form.get('reboot')
        email = request.form.get('email')
        login = request.form.get('login')
        attack = request.form.get('attack')
        limit = request.form.get('limit')
        backup = request.form.get('backup')
        update = request.files.get('update')
        load = int(request.form.get('averageLoadThreshold'))
        cpu = int(request.form.get('cpuThreshold'))
        ram = int(request.form.get('ramThreshold'))
        du = int(request.form.get('diskUsageThreshold'))
        swap = int(request.form.get('swapUsageThreshold'))
        services = request.form.get('services')

        # existing values
        services_current_value = config_data.get('DEFAULT', {}).get('services', '')
        reboot_current_value = config_data.get('DEFAULT', {}).get('reboot', '')
        login_current_value = config_data.get('DEFAULT', {}).get('login', '')
        attack_current_value = config_data.get('DEFAULT', {}).get('attack', '')
        limit_current_value = config_data.get('DEFAULT', {}).get('limit', '')
        backup_current_value = config_data.get('DEFAULT', {}).get('backup', '')
        update_current_value = config_data.get('DEFAULT', {}).get('update', '')
        load_current_value = config_data.get('DEFAULT', {}).get('load', '')
        load_current_value = int(load_current_value)
        cpu_current_value = config_data.get('DEFAULT', {}).get('cpu', '')
        cpu_current_value = int(cpu_current_value)
        ram_current_value = config_data.get('DEFAULT', {}).get('ram', '')
        ram_current_value = int(ram_current_value)
        du_current_value = config_data.get('DEFAULT', {}).get('du', '')
        du_current_value = int(du_current_value)
        swap_current_value = config_data.get('DEFAULT', {}).get('swap', '')
        swap_current_value = int(swap_current_value)

        # define
        success_messages = []
        error_messages = []
        openadmin_service_restart_is_needed = False

        # start validating and saving
        if not 1 <= load <= 100:
            error_messages.append(f"Error: '{load}' is not a valid value for load treshold! Please choose a value between 1 and 100.")
        elif load != load_current_value:
            command = f"opencli admin notifications update load '{load}'"
            success_message = f"Average Load treshold value changed."
            error_message = f"Error: Load treshold could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated load to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not 1 <= cpu <= 100:
            error_messages.append(f"Error: '{cpu}' is not a valid value for CPU treshold! Please choose a value between 1 and 100.")
        elif cpu != cpu_current_value:
            command = f"opencli admin notifications update cpu '{cpu}'"
            success_message = f"CPU% treshold value changed."
            error_message = f"Error: CPU treshold could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated cpu to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not 1 <= ram <= 100:
            error_messages.append(f"Error: '{ram}' is not a valid value for RAM treshold! Please choose a value between 1 and 100.")
        elif ram != ram_current_value:
            command = f"opencli admin notifications update ram '{ram}'"
            success_message = f"RAM treshold value changed."
            error_message = f"Error: RAM treshold could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated ram to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")


        if not 1 <= swap <= 100:
            error_messages.append(f"Error: '{swap}' is not a valid value for SWAP Usage treshold! Please choose a value between 1 and 100.")
        elif swap != swap_current_value:
            command = f"opencli admin notifications update swap '{swap}'"
            success_message = f"SWAP Usage treshold value changed."
            error_message = f"Error: SWAP Usage treshold could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated swap to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not 1 <= du <= 100:
            error_messages.append(f"Error: '{du}' is not a valid value for Disk Usage treshold! Please choose a value between 1 and 100.")
        elif du != du_current_value:
            command = f"opencli admin notifications update du '{du}'"
            success_message = f"Disk Usage treshold value changed."
            error_message = f"Error: Disk Usage treshold could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated du to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not services or not services_current_value:
            error_message = f"Error: At least one service needs to be enabled."
        elif services and services != services_current_value:
            command = f"opencli admin notifications update services '{services}'"
            success_message = f"Notification preferences for services are saved."
            error_message = f"Error: Notification preferences for services could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated services to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not update and update_current_value == 'no':
            pass
        elif update and update_current_value == 'yes':
            pass
        elif not update and update_current_value != 'no':
            command = f"opencli admin notifications update update no"
            success_message = f"Notifications on OpenPanel updates are now disabled."
            error_message = f"Error: Notifications could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated update to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif update and update_current_value != 'yes':
            command = f"opencli admin notifications update update yes"
            success_message = f"Notifications on OpenPanel updates are now enabled."
            error_message = f"Error: Notifications on OpenPanel updates could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated update to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not attack and attack_current_value != 'no':
            command = f"opencli admin notifications update attack no"
            success_message = f"Notifications on website under attack are now disabled."
            error_message = f"Error: Notifications could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated attack to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif attack and attack_current_value == 'yes':
            pass
        elif attack and attack_current_value != 'yes':
            command = f"opencli admin notifications update backup yes"
            success_message = f"Notifications on website under attack are now enabled."
            error_message = f"Error: Notifications on website under attack could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated attack to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not backup and backup_current_value != 'no':
            command = f"opencli admin notifications update backup no"
            success_message = f"Notifications on failed backups are now disabled."
            error_message = f"Error: Notifications could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated backup to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif backup and backup_current_value == 'yes':
            pass
        elif backup and backup_current_value != 'yes':
            command = f"opencli admin notifications update backup yes"
            success_message = f"Notifications on failed backups are now enabled."
            error_message = f"Error: Notifications on failed backups could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated backup to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        if not limit and limit_current_value != 'no':
            command = f"opencli admin notifications update limit no"
            success_message = f"Notifications when users reach limits are now disabled."
            error_message = f"Error: Notifications on users hitting limits could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated limit to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif limit and limit_current_value == 'yes':
            pass
        elif limit and limit_current_value != 'yes':
            command = f"opencli admin notifications update limit yes"
            success_message = f"Notifications when users reach limits are now enabled."
            error_message = f"Error: Notifications on users hitting limits could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated limit to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")



        if not login and login_current_value != 'no':
            command = f"opencli admin notifications update login no"
            success_message = f"Notifications on admin login from new ip are now disabled."
            error_message = f"Error: Notifications could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated login to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif login and login_current_value == 'yes':
            pass
        elif login and login_current_value != 'yes':
            command = f"opencli admin notifications update login yes"
            success_message = f"Notifications on admin login from new ip are now enabled."
            error_message = f"Error: Notifications on admin login could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated login to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = False
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")



        if not reboot and reboot_current_value != 'no':
            command = f"opencli admin notifications update reboot no"
            success_message = f"Notifications on server reboot are now disabled."
            error_message = f"Error: Notifications could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated reboot to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif reboot and reboot_current_value == 'yes':
            pass
        elif reboot and reboot_current_value != 'yes':
            command = f"opencli admin notifications update reboot yes"
            success_message = f"Notifications on server reboot are now enabled."
            error_message = f"Error: Notifications on server reboot could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated reboot to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")





        if not email:
            command = f"opencli config update email ''"
            success_message = f"Email alerts are disabled."
            error_message = f"Error: Email alerts could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated email to" in result:
                    success_messages.append(success_message)
                    openadmin_service_restart_is_needed = False
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        elif email:
            command = f"opencli config update email '{email}'"
            success_message = f"Email alerts are now enabled and will be send to '{email}'."
            error_message = f"Error: Email alerts could not be enabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated email to" in result:
                    success_messages.append(success_message)
                    openpanel_service_restart_is_needed = False
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")




        # return accumulated messages
        response_data = {}
        if success_messages:
            response_data["success_messages"] = success_messages
        if error_messages:
            response_data["error_messages"] = error_messages

        '''
        #
        # Temporary disabled from 0.2.2
        #
        # restart service ONLY once, if needed!
        if openadmin_service_restart_is_needed:
            openpanel_service_restart = "service admin restart" #test with reload
            try:
                subprocess.check_output(openpanel_service_restart, shell=True, text=True)
            except subprocess.CalledProcessError as e:
                response_data["openpanel_service"] = f"Error restarting OpenAdmin service: '{openpanel_service_restart}': {e.output}"
        '''
        if response_data:
            return jsonify(response_data)

    else:

        main_config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(main_config_file_path)
        email_address = config_data.get('DEFAULT', {}).get('email', '')

        config_data = {}
        with open(config_file_path, 'r') as file:
            for line in file:
                line = line.strip()
                if line.startswith('['):
                    section_title = line.strip('[]')
                elif line and '=' in line:
                    key, value = line.split('=', 1)
                    if section_title:
                        if section_title not in config_data:
                            config_data[section_title] = {}
                        config_data[section_title][key] = value

        log_dir = "/var/log/openpanel/admin"
        log_file = os.path.join(log_dir, 'notifications.log')

        notifications = None

        try:
            if os.path.exists(log_file):
                with open(log_file, 'r') as f:
                    notifications = [line.strip() for line in f.readlines() if line.strip()]
                # reverse
                notifications.sort(reverse=True)
            else:
                # create
                with open(log_file, 'w'):
                    pass
        except Exception as e:
            return f"Error loading notifications: {e}"

        return render_template('notifications.html', title='Notifications', email_address=email_address, config_data=config_data, notifications=notifications)
