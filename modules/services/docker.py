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
import subprocess
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker
from urllib.parse import unquote

@app.route('/services/docker', methods=['GET', 'POST'])
@login_required_route
def admin_gdocker_settings():
    current_route = request.path

    if request.method == 'POST':
        try:
            data = request.get_json()
            action = data.get('action')

            if action == 'update':
                result = subprocess.run(['opencli', 'docker-update_images'], capture_output=True, text=True)
                if result.returncode == 0:
                    return jsonify({'success': True, 'message': result.stdout}), 200
                else:
                    return jsonify({'success': False, 'error': result.stderr}), 500

            elif action == 'configuration':
                # TODO: these 2 end up replaced!
                cpu_usage = data.get('cpu_usage')
                ram_usage = data.get('ram_usage')

                # Execute commands to update configuration
                max_ram_command = f"opencli config update max_ram {ram_usage}"
                max_cpu_command = f"opencli config update max_cpu {cpu_usage}"

                # Execute commands and wait for completion
                max_ram_result = subprocess.run(max_ram_command, shell=True, capture_output=True, text=True)
                max_cpu_result = subprocess.run(max_cpu_command, shell=True, capture_output=True, text=True)

                # Check if both commands were successful
                if "Updated" in max_ram_result.stdout and "Updated" in max_cpu_result.stdout:
                    # Apply new limits
                    docker_limits_command = "opencli docker-limits --apply"
                    docker_limits_result = subprocess.run(docker_limits_command, shell=True, capture_output=True, text=True)

                    # Check if successful
                    if "Docker limits updated successfully" in docker_limits_result.stdout:
                        return jsonify({'success': True, 'message': 'Configuration saved successfully'}), 200
                    else:
                        return jsonify({'success': False, 'message': 'Failed to apply Docker limits'}), 500
                else:
                    return jsonify({'success': False, 'message': 'Failed to update configuration'}), 500
            

            else:
                return jsonify({'success': False, 'error': 'Invalid action'}), 400

        except Exception as e:
            return jsonify({'success': False, 'error': str(e)}), 500

    else:
        # get our images
        client = docker.from_env()
        all_images = client.images.list()
        prefix = 'openpanel/'
        filtered_images = [image for image in all_images if any(tag.startswith(prefix) for tag in image.tags)]
        image_info = [{'id': image.id, 'tags': image.tags, 'created': image.attrs['Created']} for image in filtered_images]

        # get docker ram and cpu settings
        result = subprocess.run(['opencli', 'docker-limits', '--read'], capture_output=True, text=True)
        if result.returncode == 0:
            # Parse the output to extract CPU and RAM usage percentages
            output_lines = result.stdout.strip().split('\n')
            
            if len(output_lines) > 2:
                # Ensure there are enough lines to parse
                try:
                    max_cpu_line = output_lines[1].split('=')[1].strip()
                    max_ram_line = output_lines[2].split('=')[1].strip()
                    
                    # Check if values are present and not empty
                    if max_cpu_line:
                        cpu_usage = int(max_cpu_line)
                    else:
                        cpu_usage = 90  # default
                    
                    if max_ram_line:
                        ram_usage = int(max_ram_line)
                    else:
                        ram_usage = 90  # default
                    
                except (IndexError, ValueError) as e:
                    # Handle parsing errors
                    print(f"Error parsing output: {e}")
                    cpu_usage = 90  # defaults
                    ram_usage = 90  # defaults
        else:
            # Not enough lines in the output
            print("Unexpected output format")
            cpu_usage = 90  # defaults
            ram_usage = 90  # defaults
    
        return render_template('services/docker_settings.html', title='Docker Settings', app=app, images=image_info, cpu_usage=cpu_usage, ram_usage=ram_usage, current_route = current_route)


@login_required_route
@app.route('/services/docker/delete/<path:image_name>', methods=['POST'])
def delete_docker_image(image_name):
    image_name = unquote(image_name)  # Decode URL-encoded characters
    if not image_name.startswith('openpanel/'):
        return jsonify({'error': 'Only the official OpenPanel docker images can be deleted from the OpenAdmin interface.'}), 400

    if not set(image_name).issubset(set('abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-/')):
        return jsonify({'error': 'Invalid image name.'}), 400

    command = f"docker image rm '{image_name}'"

    try:
        result = subprocess.run(command, shell=True, capture_output=True, text=True)

        if result.returncode == 0:
            return jsonify({'success': True, 'output': result.stdout.strip()})
        else:
            return jsonify({'success': False, 'error': result.stderr.strip()}), 400

    except Exception as e:
        return jsonify({'success': False, 'error': str(e)}), 500

