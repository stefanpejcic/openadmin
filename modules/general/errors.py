# import flask modules
from flask import Flask, request, jsonify, render_template
import logging
from logging.handlers import RotatingFileHandler
import os

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


# 404
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

    else:
        #return jsonify({'error': 'Resource not found'}), 404
        return render_template('system/404.html'), 404

# 500
@app.errorhandler(500)
def internal_error(error):
    error_logger.error(f'500 error: {error}', exc_info=True)
    return jsonify({'error': 'Internal server error'}), 500

# all other..
@app.errorhandler(Exception)
def handle_errors(error):
    error_logger.error(f'Error: {error}', exc_info=True)
    return jsonify({'error': 'An unexpected error occurred'}), 500
