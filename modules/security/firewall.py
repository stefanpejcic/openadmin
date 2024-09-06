################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 10.06.2024
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
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, send_file, make_response
import subprocess
import datetime
import psutil
from app import app, is_license_valid, login_required_route, load_openpanel_config
import modules.helpers
import tempfile

# added in 0.2.3 support for both csf and ufw
def is_command_available(command):
    """Check if a command is available on the system."""
    try:
        subprocess.run([command, '--version'], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False

@app.route('/configservercsf/iframe/', methods=['GET', 'POST'])
@login_required_route
def configservercsfiframe():
    try:
        if request.method == 'GET':
            qs = '&'.join([f"{key}={value}" for key, value in request.args.items()])
        elif request.method == 'POST':
            qs = '&'.join([f"{key}={value}" for key, value in request.form.items()])

        # FAILED fix for: https://bytetool.web.app/en/ascii/code/0xa9/
        # Unable to create csf UI temp file: 'utf-8' codec can't decode byte 0xa9 in position 1446: invalid start byte
        qs_encoded = qs.encode('utf-8', errors='ignore').decode('utf-8', errors='delete')


        #print(f"DEBUG: Encoded data length: {len(qs_encoded)}")

        with tempfile.NamedTemporaryFile(mode="w", delete=False, encoding='utf-8') as tmp:
            tmp.write(qs_encoded)
            tmp_name = tmp.name

        #print(f"DEBUG: Temporary file created: {tmp_name}")

        command = f"/usr/local/admin/modules/security/csf.pl '{tmp_name}'"

        try:
            # Use subprocess to execute the command
            result = subprocess.run(command, shell=True, capture_output=True, text=True)
            output = result.stdout
        except subprocess.CalledProcessError as e:
            output = f"Output Error from csf UI script: {e}"
        finally:
            if os.path.exists(tmp_name):
                os.unlink(tmp_name)

    except Exception as e:
        output = f"Unable to create csf UI temp file: {e}"

    return make_response(output)









def get_ufw_status():
    try:
        # Get UFW status
        result = subprocess.run(['sudo', 'ufw', 'status'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        ufw_status = "active" if "Status: active" in result.stdout else "inactive"

        # Read /etc/default/ufw file and extract IPV6, DEFAULT_INPUT_POLICY, and DEFAULT_OUTPUT_POLICY
        default_ufw_status = {}
        with open('/etc/default/ufw', 'r') as f:
            for line in f:
                if line.startswith(('IPV6=', 'DEFAULT_INPUT_POLICY=', 'DEFAULT_OUTPUT_POLICY=')):
                    key, value = line.strip().split('=')
                    default_ufw_status[key] = value.strip('"')

        # Check for IPMI ACCEPT or DROP rules in /etc/ufw/before.rules
        ipmi_status = "not found"
        try:
            with open('/etc/ufw/before.rules', 'r') as f:
                for line in f:
                    if "-A ufw-before-input -p icmp --icmp-type destination-unreachable -j ACCEPT" in line:
                        ipmi_status = "ACCEPT"
                        break
                    elif "-A ufw-before-input -p icmp --icmp-type destination-unreachable -j DROP" in line:
                        ipmi_status = "DROP"
                        break
        except FileNotFoundError:
            ipmi_status = "/etc/ufw/before.rules file not found"
        
        default_ufw_status["IPMI"] = ipmi_status

        return {
            "status": ufw_status,
            "settings": default_ufw_status
        }
    except FileNotFoundError:
        return {"error": "/etc/default/ufw file not found"}
    except subprocess.CalledProcessError as e:
        return {"error": f"Command error: {e}"}
    except Exception as e:
        return {"error": str(e)}

def set_ufw_status(action):
    try:
        if action == 'enable':
            result = subprocess.run(['sudo', 'ufw', 'enable'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        elif action == 'disable':
            result = subprocess.run(['sudo', 'ufw', 'disable'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        else:
            return "Invalid action", 400
        return result.stdout
    except Exception as e:
        return str(e), 500


def get_firewall_rules():
    try:
        # Use a single subprocess call to run both 'ufw status numbered' and 'jc --ufw'
        cmd = 'ufw status numbered | jc --ufw'
        result = subprocess.check_output(cmd, shell=True, universal_newlines=True)
        data = json.loads(result)

        # Extract the rules from the JSON data
        firewall_rules = data.get('rules', [])
        firewall_status = data.get('status', '')

        return firewall_rules, firewall_status

    except subprocess.CalledProcessError as e:
        return [{'error': str(e)}]



def parse_line(line):
    line = line.strip()
    status = "disabled" if line.startswith('#') else "enabled"
    line = line.lstrip('#').strip()  # Remove the leading '#' if present
    parts = line.split('|')
    if len(parts) == 2:
        return {"name": parts[0], "url": parts[1], "status": status}
    elif len(parts) == 3:
        return {"name": parts[0], "url": parts[1], "api_key": parts[2], "status": status}
    else:
        return None  # Handle invalid format


def set_ufw_settings(settings):
    try:
        # Read existing settings from /etc/default/ufw
        with open('/etc/default/ufw', 'r') as f:
            lines = f.readlines()

        # Update settings in /etc/default/ufw
        for key, value in settings.items():
            if key == "IPMI_STATUS":
                continue  # Skip IPMI_STATUS for this file
            for i, line in enumerate(lines):
                if line.startswith(key):
                    value = value.upper()
                    lines[i] = f"{key}=\"{value}\"\n"
                    break

        # Write updated settings back to /etc/default/ufw
        with open('/etc/default/ufw', 'w') as f:
            f.writelines(lines)

        # Update IPMI rules in /etc/ufw/before.rules if needed
        if "IPMI_STATUS" in settings:
            ipmi_new_status = settings["IPMI_STATUS"].upper()
            ipmi_rules = [
                "-A ufw-before-input -p icmp --icmp-type destination-unreachable -j ",
                "-A ufw-before-input -p icmp --icmp-type time-exceeded -j ",
                "-A ufw-before-input -p icmp --icmp-type parameter-problem -j ",
                "-A ufw-before-input -p icmp --icmp-type echo-request -j "
            ]

            with open('/etc/ufw/before.rules', 'r') as f:
                before_rules_lines = f.readlines()

            # Replace existing IPMI rules
            for i, line in enumerate(before_rules_lines):
                for rule in ipmi_rules:
                    if line.startswith(rule):
                        before_rules_lines[i] = f"{rule}{ipmi_new_status}\n"

            # Write updated settings back to /etc/ufw/before.rules
            with open('/etc/ufw/before.rules', 'w') as f:
                f.writelines(before_rules_lines)

        # Restart UFW to apply changes
        subprocess.run(['ufw', 'reload'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

        return {"message": "Settings updated successfully"}
    except Exception as e:
        return {"error": str(e)}

@app.route('/security/firewall/manage', methods=['GET', 'POST'])
@login_required_route
def manage_firewall():
    if request.method == 'GET':
        status = get_ufw_status()
        return jsonify({"ufw": status})
    
    elif request.method == 'POST':
        data = request.json
        action = data.get('action')

        if action:
            if action == 'export_ipv4':
                # Read content of /etc/ufw/user.rules
                file_path = '/etc/ufw/user.rules'
                return send_file(file_path, as_attachment=True)
            elif action == 'export_ipv6':
                # Read content of /etc/ufw/user6.rules
                file_path = '/etc/ufw/user6.rules'
                return send_file(file_path, as_attachment=True)
            elif action == 'reset-openpanel-user-ports':
                # Run command: opencli firewall-reset
                result = os.system('opencli firewall-reset')
                if result == 0:
                    return jsonify({"result": "OpenPanel user ports reset successful"})
                else:
                    return jsonify({"error": "Failed to reset OpenPanel user ports"}), 500
            elif action not in ['enable', 'disable']:
                return jsonify({"error": "Invalid action"}), 400
            
            result = set_ufw_status(action)
            return jsonify({"result": result})
        else:
            settings = data.get('settings')
            if settings:
                result = set_ufw_settings(settings)
                return jsonify(result)
            else:
                return jsonify({"error": "No action or settings provided"}), 400


@app.route('/security/firewall/blacklists', methods=['GET', 'POST'])
@login_required_route
def manage_blacklists():
    if request.method == 'GET':
        try:
            with open('/etc/openpanel/ufw/blacklists.conf', 'r') as blacklist_file:
                content = blacklist_file.read()
            lines = content.split('\n')
            parsed_lines = [parse_line(line) for line in lines if line.strip()]
            return jsonify({"status": "success", "content": parsed_lines})
        except FileNotFoundError:
            return jsonify({"status": "error", "message": "Blacklist file not found."}), 404
    elif request.method == 'PATCH':
        new_content = request.json.get('content')
        if new_content is not None and isinstance(new_content, list):
            try:
                with open('/etc/openpanel/ufw/blacklists.conf', 'w') as blacklist_file:
                    for entry in new_content:
                        if 'name' in entry and 'url' in entry:
                            line = f"{entry['name']}|{entry['url']}"
                            if 'api_key' in entry:
                                line += f"|{entry['api_key']}"
                            if entry.get('status') == 'disabled':
                                line = f"#{line}"
                            blacklist_file.write(line + '\n')
                        else:
                            return jsonify({"status": "error", "message": "Invalid entry format."}), 400
                return jsonify({"status": "success", "message": "Blacklist updated successfully."})
            except Exception as e:
                return jsonify({"status": "error", "message": f"An error occurred while updating the blacklist: {e}"}), 500
        else:
            return jsonify({"status": "error", "message": "Invalid content provided."}), 400


    elif request.method == 'POST':
        action = request.form.get('action')
        if action == 'add':
            blacklist_entry = request.form.get('blacklist_entry')
            subprocess.run(['opencli', 'blacklist', '--add-blacklist=' + blacklist_entry])
            return "Added to blacklist: " + blacklist_entry
        elif action == 'delete':
            blacklist_entry = request.form.get('blacklist_entry')
            subprocess.run(['opencli', 'blacklist', '--delete-blacklist=' + blacklist_entry])
            return "Deleted from blacklist: " + blacklist_entry
        elif action == 'disable':
            subprocess.run(['opencli', 'blacklist', '--disable-blacklist'])
            return "Blacklist disabled."
        elif action == 'enable':
            subprocess.run(['opencli', 'blacklist', '--enable-blacklist'])
            return "Blacklist enabled."
        elif action == 'run':
            subprocess.run(['opencli', 'blacklist', '--fetch', '--update-ufw'])
            return "Blacklist updated and applied to UFW."
        else:
            return "Invalid action."


# check if cf rules are enabled
#
# https://dev.openpanel.com/cli/cloudflare.html
#
def check_cloudflare_rules():
    ufw_status = subprocess.run(['ufw', 'status'], capture_output=True, text=True)
    lines = ufw_status.stdout.split('\n')
    for line in lines:
        if "Cloudflare IP" in line:
            return True
    return False

@app.route('/security/firewall', methods=['GET', 'POST'])
@login_required_route
def firewall_settings():
    if request.method == 'GET':
        if is_command_available('csf'):
            return render_template('security/csf.html')
        elif is_command_available('ufw'):
            firewall_rules, firewall_status = get_firewall_rules()
            current_route = request.path
            
            # Check if /usr/ipset-blacklist.sh exists, if it does, we will also load that module
            # added in 0.1.9
            ipset_blacklist_exists = os.path.exists('/usr/ipset-blacklist.sh')
            cloudflare_status = check_cloudflare_rules()

            # Check if IPV6 is enabled in ufw settings
            # added in 0.2.1
            is_ipv6_enabled = False
            with open('/etc/default/ufw', 'r') as ufw_config:
                for line in ufw_config:
                    if line.strip().startswith('IPV6='):
                        stripped_line = line.strip()
                        if stripped_line.startswith('#'):
                            continue  # Ignore commented lines
                        is_ipv6_enabled = stripped_line.split('=')[1].strip('"').upper() == "YES"
                        break

            return render_template('security/firewall_settings.html', 
                                title="Firewall", 
                                is_ipv6_enabled=is_ipv6_enabled,
                                current_route=current_route, 
                                firewall_rules=firewall_rules, 
                                firewall_status=firewall_status,
                                cloudflare_status=cloudflare_status,
                                ipset_blacklist_exists=ipset_blacklist_exists)

        else:
            return "Neither CSF nor UFW is available on this system.", 404

        # enable option to enable/disable cloudflare ipset
        # added in 0.1.9
    elif request.method == 'POST':
        action = request.form.get('action')
        if action == 'enable-cloudflare':
            subprocess.run(['opencli', 'cloudflare', '--enable'])
            flash('Server access is now restricted to Cloudflare IP addresses only.', 'success')
        elif action == 'disable-cloudflare':
            subprocess.run(['opencli', 'cloudflare', '--disable'])
            flash('Server access is now NOT restricted to Cloudflare IP addresses only.', 'success')
        else:
            flash('Invalid action.', 'danger')
        return redirect(url_for('firewall_settings'))
    else:
        return jsonify({'message': 'Invalid request type.'})


@app.route('/security/firewall/delete-rule/<int:rule_number>', methods=['POST'])
@login_required_route
def delete_firewall_rule(rule_number):
    if is_command_available('ufw'):
        try:
            ufw_command = ['ufw', 'delete', str(rule_number)]
            #print(f"Executing command: {' '.join(ufw_command)}")
            process = subprocess.run(ufw_command, input='yes\n', stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=True)
            if process.returncode == 0:
                print(f"Command output: {process.stdout}")
                return jsonify({'message': 'Rule deleted successfully'})
        except subprocess.CalledProcessError as e:
            print(f"Command returned non-zero exit code: {e.returncode}")
            return jsonify({'message': f'Error deleting rule: {e.stderr}'}, 500)
        except subprocess.TimeoutExpired:
            print("Command timed out")
            return jsonify({'message': 'Command timed out'}, 500)

        return jsonify({'message': 'An unknown error occurred'}, 500)
    else:
        return jsonify({'message': 'UFW is not present!'}, 500)




@app.route('/security/firewall/add-rule', methods=['POST'])
@login_required_route
def add_firewall_rule():
    if is_command_available('ufw'):
        action = request.form.get('action')  # 'allow' or 'deny'
        input_data = request.form.get('input_data')

        try:
            # Port range
            if ':' in input_data and all(part.isdigit() for part in input_data.split(':')):
                port_range = input_data.split(':')
                if action == 'allow':
                    ufw_command = ['ufw', 'allow', f'{port_range[0]}:{port_range[1]}/tcp']
                elif action == 'deny':
                    ufw_command = ['ufw', 'deny', f'{port_range[0]}:{port_range[1]}/tcp']
                else:
                    return jsonify({'message': 'Invalid action'}, 400)
            elif input_data.isdigit():
                # Single port
                if action == 'allow':
                    ufw_command = ['ufw', 'allow', f'{input_data}/tcp']
                elif action == 'deny':
                    ufw_command = ['ufw', 'deny', f'{input_data}/tcp']
                else:
                    return jsonify({'message': 'Invalid action'}, 400)
            else:
                if action == 'allow':
                    ufw_command = ['ufw', 'allow', 'from', f'{input_data}']
                elif action == 'deny':
                    ufw_command = ['ufw', 'deny', 'from', f'{input_data}']
                else:
                    return jsonify({'message': 'Invalid action'}, 400)

            #print(ufw_command)
            process = subprocess.run(ufw_command, input='yes\n', stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=True)

            if process.returncode == 0:
                return jsonify({'message': 'Rule added successfully'})
        except subprocess.CalledProcessError as e:
            return jsonify({'message': f'Error adding rule: {e.stderr}'}, 500)
        except subprocess.TimeoutExpired:
            return jsonify({'message': 'Command timed out'}, 500)

        return jsonify({'message': 'An unknown error occurred'}, 500)

    else:
        return jsonify({'message': 'UFW is not present!'}, 500)




@app.route('/json/ufw-log', methods=['GET'])
@login_required_route
def get_ufw_log():
    if is_command_available('ufw'):
        try:
            log_output = subprocess.check_output(['tail', '-1000', '/var/log/ufw.log'], universal_newlines=True)
            log_lines = log_output.split('\n')
            
            return jsonify({'ufw_log': log_lines})

        except subprocess.CalledProcessError as e:
            return jsonify({'error': f"Error running command: {e}"})

    else:
        return jsonify({'message': 'UFW is not present!'}, 500)
