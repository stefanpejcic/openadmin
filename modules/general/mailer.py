from flask import request, redirect, url_for, jsonify, render_template
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:18                                       *
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
import json
import psutil
from flask_mail import Mail, Message
from app import load_openpanel_config, cache, csrf,  get_openpanel_port, get_openpanel_domain
from modules.helpers import get_counts_from_db
import subprocess
import datetime
import socket
import os
import requests

# Load the configuration data
config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
config_data = load_openpanel_config(config_file_path)



# Function to save email content to a file
def save_email_to_file(subject, recipient, body):
    log_folder = '/var/log/openpanel/admin/emails/'
    # Create the folder if it doesn't exist
    os.makedirs(log_folder, exist_ok=True)
    # Create a filename based on current timestamp or some unique identifier
    filename = f"{recipient}_{subject}.txt"
    filepath = os.path.join(log_folder, filename)
    # Write the email content to the file
    with open(filepath, 'w') as file:
        file.write(body)

#helper function used if domain is not set for panel access
@cache.memoize(timeout=3600)
def get_public_ip():
    try:
        response = requests.get("https://ip.openpanel.com")
        response.raise_for_status()
        ip = response.text.strip()
        # Check if the received IP is a valid IPv4 address
        parts = ip.split('.')
        if len(parts) == 4 and all(0 <= int(part) < 256 for part in parts):
            return ip
        else:
            raise ValueError("Invalid IPv4 address received")
    except (requests.RequestException, ValueError) as e:
        print("Failed to retrieve public IP:", e)
        # Fallback to retrieving local IP
        try:
            output = subprocess.check_output(["hostname", "-I"]).decode().strip()
            # Extract the first IP address from the output
            ip_address = output.split()[0]
            return ip_address
        except Exception as e:
            print("Failed to retrieve local IP:", e)
            return None


# Open configuration file
#
#  Helper function to read and parse conf file
#
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


'''
curl -X POST   https://175.openpanel.site:2087/send_email  \
-F 'transient=6g732dg236ggd2gaeqer32f323f2wew' \
-F 'recipient=stefan@netops.com' \
-F 'subject=proba sa servera' \
-F 'body=Da li je dosao mejl? Hvala.'
'''


#
def validate_one_time_code(one_time_code):
    # Load the configuration data
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)
    code_unique_for_server = config_data.get('SMTP', {}).get('mail_security_token')
    return one_time_code == code_unique_for_server





def get_cpu_usage():
    try:
        # Get the CPU usage percentage
        cpu_usage = psutil.cpu_percent(interval=1)
        return cpu_usage
    except Exception as e:
        print(f"An unexpected error occurred: {e}")
        return None

def get_disk_usage():
    try:
        partitions = psutil.disk_partitions(all=False)
        
        total_disk_gb = 0.0
        used_disk_gb = 0.0
        
        for partition in partitions:
            usage = psutil.disk_usage(partition.mountpoint)
            total_disk_gb += usage.total / (1024.0 ** 3)
            used_disk_gb += usage.used / (1024.0 ** 3)
        
        if total_disk_gb > 0:
            used_ratio = used_disk_gb / total_disk_gb
            used_formatted = f"{used_disk_gb:.2f} / {total_disk_gb:.2f} GB"
        else:
            used_ratio = 0.0
            used_formatted = "0.00 / 0.00 GB"
        
        return used_formatted
    
    except Exception as e:
        print(f"An unexpected error occurred: {e}")
        return "N/A"

def get_ram_usage():
    try:
        memory = psutil.virtual_memory()
        
        total_memory_gb = memory.total / (1024.0 ** 3)
        used_memory_gb = memory.used / (1024.0 ** 3)
        
        used_formatted = f"{used_memory_gb:.2f} / {total_memory_gb:.2f} GB"
        
        return used_formatted
    
    except Exception as e:
        print(f"An unexpected error occurred: {e}")
        return "N/A"

def count_crons_executed_today():
    today = datetime.datetime.today().strftime('%a %b %d')  # Example: 'Sun May  4'
    count = 0
    with open('/var/log/openpanel/admin/cron.log') as f:
        for line in f:
            if today in line and "Notifications script executed" not in line:
                count += 1
    return count

def count_api_requests_received_today():
    today = datetime.datetime.today().strftime('%Y-%m-%d')  # Example: '2025-05-04'
    count = 0
    with open('/var/log/openpanel/admin/api.log') as f:
        for line in f:
            if today in line:
                count += 1
    return count

####### end helpers for daily reports







def smtp_mailer(app):

    # Load the configuration data
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)

    # Define default values
    default_config = {
        'mail_server': 's34.unlimited.rs',
        'mail_port': 465,
        'mail_use_ssl': True,
        'mail_username': 'no-reply@openpanel.com',
        'mail_password': 'jJakwVSA3BFxJIN4A',
        'mail_default_sender': 'no-reply@openpanel.com'
    }

    for key, value in default_config.items():
        if not config_data.get('SMTP', {}).get(key):
            config_data.setdefault('SMTP', {})[key] = value

    # Configuration for Flask-Mail
    app.config['MAIL_SERVER'] = config_data.get('SMTP', {}).get('mail_server')
    app.config['MAIL_PORT'] = int(config_data.get('SMTP', {}).get('mail_port'))
    app.config['MAIL_USE_SSL'] = config_data.get('SMTP', {}).get('mail_use_ssl')
    app.config['MAIL_USERNAME'] = config_data.get('SMTP', {}).get('mail_username')
    app.config['MAIL_PASSWORD'] = config_data.get('SMTP', {}).get('mail_password')
    app.config['MAIL_DEFAULT_SENDER'] = config_data.get('SMTP', {}).get('mail_default_sender')

    mail = Mail(app)

    @app.route('/send_email', methods=['POST'])
    @csrf.exempt
    def send_email():
        if request.method == 'POST':
            #if request.remote_addr != '127.0.0.1' and request.remote_addr != '::1':
            #    abort(403)  # Forbidden
            recipient = request.form['recipient']
            subject = request.form['subject']
            message_content = request.form['body']
            server_hostname = socket.gethostname() or  subprocess.check_output(["hostname"]).decode("utf-8").strip()

            if 'OpenPanel URL' not in message_content:
                # Validate the unique code on all pages except user add
                one_time_code = request.form.get('transient') 
                if not validate_one_time_code(one_time_code):
                    return jsonify({"error": "Invalid unique code", "transient": one_time_code}), 401

            # Load the configuration data
            config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
            config_data = load_openpanel_config(config_file_path)


            port = get_openpanel_port()

            domain_forces = get_openpanel_domain()

            if domain_forces:
                protocol = 'https://'
            else:
                protocol = 'http://'
                domain_forces = get_public_ip()

            message_title = "[" + domain_forces + "] " + request.form['subject'] 

            admin_url = protocol + domain_forces + ":2087/"
            panel_notifications_page = protocol + domain_forces + ":" + port + "/settings/notifications"

            if 'Daily Usage Report' in message_content:
                # custom template for daily reports
                counts = get_counts_from_db()
                total_users = counts[0] if counts and len(counts) >= 1 else 0
                total_websites = counts[2] if counts and len(counts) >= 3 else 0
                total_domains = counts[3] if counts and len(counts) >= 4 else 0

                current_disk_usage = get_disk_usage()
                total_cpu_usage = get_cpu_usage()
                total_ram_usage = get_ram_usage()
                crons_executed_today = count_crons_executed_today()
                api_requests_received_today = count_api_requests_received_today()

                email_template = render_template(
                    'system/email_system_report.html',
                    title=subject,
                    message=message_content,
                    hostname=server_hostname,
                    admin_url=admin_url,
                    total_users=total_users,
                    total_domains=total_domains,
                    total_websites=total_websites,
                    current_disk_usage=current_disk_usage,
                    total_cpu_usage=f"{total_cpu_usage:.2f} %", 
                    total_ram_usage=total_ram_usage,
                    crons_executed_today=crons_executed_today,
                    api_requests_received_today=api_requests_received_today
                )

            elif 'OpenPanel URL' in message_content:
                # send email to user after creating account
                email_template = render_template('system/new_user_email.html', title=subject, message=message_content, hostname=server_hostname)
            elif 'changed for account' in message_content or  "login for account" in message_content:
                email_template = render_template('system/user_notification_email.html', title=subject, notifications_url=panel_notifications_page, message=message_content, hostname=server_hostname)
            else:
                # all other emails: notifications, admin logins and updates
                email_template = render_template('system/email_template.html', title=subject, message=message_content, hostname=server_hostname, admin_url=admin_url)


            try:
                # Create a message
                message = Message(subject=message_title,
                                recipients=[recipient],
                                html=email_template)

                # Save the email to file before sending
                ###### WORKS, just needs gui! 
                # save_email_to_file(subject, recipient, email_template)

                # Send the message
                mail.send(message)
                if 'OpenPanel URL' in message_content:
                    return "Email sent successfully"
                else:
                    return jsonify({"message": "Email sent successfully"}), 200
            except Exception as e:

                dev_mode = config_data.get('PANEL', {}).get('dev_mode', 'off') # off for versions <0.1.6

                if dev_mode.lower() == 'on':

                    # Construct error response with all relevant information
                    error_info = {
                        "error": f"An error occurred: {str(e)}",
                        "recipient": recipient,
                        "subject": subject,
                        "body": message_content,
                        "mail_server": app.config['MAIL_SERVER'],
                        "mail_port": app.config['MAIL_PORT'],
                        #"mail_use_tls": app.config['MAIL_USE_TLS'],
                        "mail_use_ssl": app.config['MAIL_USE_SSL'],
                        "mail_username": app.config['MAIL_USERNAME'],
                        #"mail_password": app.config['MAIL_PASSWORD'], #should not be shown, ever!
                        "mail_default_sender": app.config['MAIL_DEFAULT_SENDER']
                    }
                    return jsonify(error_info), 500  
                else:
                    return jsonify({"error": f"An error occurred: {str(e)}"}), 500
