# import flask modules
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:19                                       *
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
from flask import Flask, request, jsonify, render_template
import logging
from logging.handlers import RotatingFileHandler
import os
from flask_wtf.csrf import CSRFError

# import our modules
from app import app, load_openpanel_config

# logs
log_directories = ['/var/log/openpanel/admin', '/var/log/openpanel/user']
for log_directory in log_directories:
    if not os.path.exists(log_directory):
        os.makedirs(log_directory)

# format
log_format = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')

# API Logger
api_log_path = os.path.join('/var/log/openpanel/admin', 'api.log')
api_handler = RotatingFileHandler(api_log_path, maxBytes=100000, backupCount=10)
api_handler.setFormatter(log_format)
api_logger = logging.getLogger('api_logger')
api_logger.setLevel(logging.INFO)
api_logger.addHandler(api_handler)
api_logger.propagate = False

# Error Logger
error_log_path = os.path.join('/var/log/openpanel/admin', 'error.log')
error_handler = RotatingFileHandler(error_log_path, maxBytes=100000, backupCount=10)
error_handler.setFormatter(log_format)
error_logger = logging.getLogger('error_logger')
error_logger.setLevel(logging.ERROR)
error_logger.addHandler(error_handler)
error_logger.propagate = False

# uised on all error handlers
def render_error_template(error_message, error_code):
    return render_template('system/error.html', error_message=error_message, error_code=str(error_code)), error_code



# individual handlers
@app.errorhandler(404)
def custom_404_error_handler(error):
    if request.path.startswith('/api/'):
        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(config_file_path)
        api_status_current_value = config_data.get('PANEL', {}).get('api', 'off')

        if api_status_current_value.lower() == 'on':
            return jsonify({'error': 'This api route does not exist. Please check the documentation: https://dev.openpanel.com/api/'}), 404        
        else:
            return jsonify({'error': 'API access is disabled! To enable api access OpenAdmin > Settings'}), 404

    return render_error_template('Page not found', 404)


@app.errorhandler(403)
def handle_403(error):
    error_logger.error(f'403 error: {error}', exc_info=True)
    return render_error_template('Access is forbidden', 403)

@app.errorhandler(500)
def handle_500(error):
    error_logger.error(f'500 error: {error}', exc_info=True)
    return render_error_template('An unexpected error occurred', 500)

@app.errorhandler(CSRFError)
def handle_csrf_error(error):
    return jsonify({"error": "CSRF error", "message": error.description}), 400

@app.errorhandler(Exception)
def handle_exception(error):
    error_logger.error(f'Unhandled exception: {error}', exc_info=True)
    return render_error_template('An unexpected error occurred', 500)
