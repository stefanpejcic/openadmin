#!/bin/bash

# notifications conf file
CONF_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
# swap lock file to not repeat cleanup
LOCK_FILE="/tmp/swap_cleanup.lock"
TIME=$(date)
# main conf file
INI_FILE="/etc/openpanel/openadmin/config/notifications.ini"

# Check if the INI file exists
if [ ! -f "$INI_FILE" ]; then
    echo "Error: INI file not found: $INI_FILE"
    exit 1
fi


# helper function to generate random token
generate_random_token() {
    tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 64
}

generate_random_token_one_time_only() {
    TOKEN_ONE_TIME="$(generate_random_token)"
    local new_value="mail_security_token=$TOKEN_ONE_TIME"
    # Use sed to replace the line in the file
    sed -i "s|^mail_security_token=.*$|$new_value|" "$CONF_FILE"
}

# Function to check if a value is a number between 1 and 100
is_valid_number() {
  local value="$1"
  [[ "$value" =~ ^[1-9][0-9]?$|^100$ ]]
}

# Extract email address from the configuration file
EMAIL_ALERT=$(awk -F'=' '/^email/ {print $2}' "$CONF_FILE")

# If email address is found, set EMAIL_ALERT to "yes" and set EMAIL to that address
if [ -n "$EMAIL_ALERT" ]; then
    EMAIL=$EMAIL_ALERT
    EMAIL_ALERT=yes
else
    # If no email address is found, set EMAIL_ALERT to "no" by default
    EMAIL_ALERT=no
fi



# Read values from the INI file or set fallback values

REBOOT=$(awk -F'=' '/^reboot/ {print $2}' "$INI_FILE")
REBOOT=${REBOOT:-yes}
[[ "$REBOOT" =~ ^(yes|no)$ ]] || REBOOT=yes

LOGIN=$(awk -F'=' '/^login/ {print $2}' "$INI_FILE")
LOGIN=${LOGIN:-yes}
[[ "$LOGIN" =~ ^(yes|no)$ ]] || LOGIN=yes

ATTACK=$(awk -F'=' '/^attack/ {print $2}' "$INI_FILE")
ATTACK=${ATTACK:-yes}
[[ "$ATTACK" =~ ^(yes|no)$ ]] || ATTACK=yes

LIMIT=$(awk -F'=' '/^limit/ {print $2}' "$INI_FILE")
LIMIT=${LIMIT:-yes}
[[ "$LIMIT" =~ ^(yes|no)$ ]] || LIMIT=yes

BACKUP=$(awk -F'=' '/^backup/ {print $2}' "$INI_FILE")
BACKUP=${BACKUP:-yes}
[[ "$BACKUP" =~ ^(yes|no)$ ]] || BACKUP=yes

UPDATE=$(awk -F'=' '/^update/ {print $2}' "$INI_FILE")
UPDATE=${UPDATE:-yes}
[[ "$UPDATE" =~ ^(yes|no)$ ]] || UPDATE=yes

SERVICES=$(awk -F'=' '/^services/ {print $2}' "$INI_FILE")
SERVICES=${SERVICES:-"admin,docker,mysql,ufw,panel"}

LOAD_THRESHOLD=$(awk -F'=' '/^load/ {print $2}' "$INI_FILE")
LOAD_THRESHOLD=${LOAD_THRESHOLD:-20}
is_valid_number "$LOAD_THRESHOLD" || LOAD_THRESHOLD=20

CPU_THRESHOLD=$(awk -F'=' '/^cpu/ {print $2}' "$INI_FILE")
CPU_THRESHOLD=${CPU_THRESHOLD:-90}
is_valid_number "$CPU_THRESHOLD" || CPU_THRESHOLD=90

RAM_THRESHOLD=$(awk -F'=' '/^ram/ {print $2}' "$INI_FILE")
RAM_THRESHOLD=${RAM_THRESHOLD:-85}
is_valid_number "$RAM_THRESHOLD" || RAM_THRESHOLD=85

DISK_THRESHOLD=$(awk -F'=' '/^du/ {print $2}' "$INI_FILE")
DISK_THRESHOLD=${DISK_THRESHOLD:-85}
is_valid_number "$DISK_THRESHOLD" || DISK_THRESHOLD=85

SWAP_THRESHOLD=$(awk -F'=' '/^swap/ {print $2}' "$INI_FILE")
SWAP_THRESHOLD=${SWAP_THRESHOLD:-40}
is_valid_number "$SWAP_THRESHOLD" || SWAP_THRESHOLD=40


# hostname
HOSTNAME=$(hostname)

# Path to the log file
LOG_FILE="/var/log/openpanel/admin/notifications.log"


# Function to get the last message content from the log file
get_last_message_content() {
  tail -n 1 "$LOG_FILE" 2>/dev/null
}

# Function to check if an unread message with the same content exists in the log file
is_unread_message_present() {
  local unread_message_content="$1"
  grep -q "UNREAD.*$unread_message_content" "$LOG_FILE" && return 0 || return 1
}

# Send an email alert
email_notification() {
  local title="$1"
  local message="$2"


  #set random token
  generate_random_token_one_time_only

  # use the token
  TRANSIENT=$(awk -F'=' '/^mail_security_token/ {print $2}' "$CONF_FILE")

  echo $TRANSIENT

# curl -k -X POST   https://127.0.0.1:2087/send_email  -F 'transient=z3t5LPt4HirqpmW1KHbZdEXtgNR4Rl4bIw6xv4irUZIxXkIXZ8SJHjduOhjvDEe8' -F 'recipient=stefan@pejcic.rs' -F 'subject=proba sa servera' -F 'body=Da li je dosao mejl? Hvala.'


# Check for SSL
SSL=$(awk -F'=' '/^ssl/ {print $2}' "$CONF_FILE")

# Determine protocol based on SSL configuration
if [ "$SSL" = "yes" ]; then
  PROTOCOL="https"
else
  PROTOCOL="http"
fi

# Send email using appropriate protocol
curl -k -X POST "$PROTOCOL://127.0.0.1:2087/send_email" -F "transient=$TRANSIENT" -F "recipient=$EMAIL" -F "subject=$title" -F "body=$message"

}

# Function to write notification to log file if it's different from the last message content
write_notification() {
  local title="$1"
  local message="$2"
  local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
  local last_message_content=$(get_last_message_content)

  # Check if the current message content is the same as the last one and has "UNREAD" status
  if [ "$message" != "$last_message_content" ] && ! is_unread_message_present "$title"; then
    echo "$current_message" >> "$LOG_FILE"
    if [ "$EMAIL_ALERT" != "no" ]; then
      email_notification "$title" "$message"
    else
      echo "Email alerts are disabled."
    fi


  fi
}

# Function to perform startup action (system reboot notification)
perform_startup_action() {
  if [ "$REBOOT" != "no" ]; then
    local title="SYSTEM REBOOT!"
    local uptime=$(uptime)
    local message="System was rebooted. $uptime"
    write_notification "$title" "$message"
  else
    echo "Reboot is explicitly set to 'no' in the INI file. Skipping logging.."
  fi
}

# Notify when admin account is accessed from new IP address
check_new_logins() {
  if [ "$LOGIN" != "no" ]; then
    touch /var/log/openpanel/admin/login.log
    # Extract the last line from the login log file
    last_login=$(tail -n 1 /var/log/openpanel/admin/login.log)
        
    # Skip empty lines
    if [ -z "$last_login" ]; then
      return 1
    fi

    # Parse username and IP address from the last login entry
    username=$(echo "$last_login" | awk '{print $3}')
    ip_address=$(echo "$last_login" | awk '{print $4}')

    # Validate IP address format
    if [[ ! $ip_address =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "Invalid IP address format: $ip_address"
      return 1
    fi



    # Check if the username appears more than once in the log file
    if [ $(grep -c $username /var/log/openpanel/admin/login.log) -eq 1 ]; then
      echo "First time login detected for user: $username. Skipping IP address check."
    else
      # Check if the combination of username and IP address has appeared before
      if ! grep -q "$username $ip_address" <(sed '$d' /var/log/openpanel/admin/login.log); then
        echo "Admin account $username accessed from new IP address, writing notification.."
        local title="Admin account $username accessed from new IP address"
        local message="Admin account $username was accessed from a new IP address: $ip_address"
        write_notification "$title" "$message"
      else
        echo "Admin account $username accessed from previously logged IP address: $ip_address. Skipping notification."
      fi
    fi
  else
    echo "New login detected fro admin user: $username from IP: $ip_address, but notifications are disabled by admin user. Skipping logging."
  fi
}



panel_docker_containers_status() {

      # Check if the OpenPanel Docker container is running
      if docker ps --format "{{.Names}}" | grep -q "openpanel"; then
        echo "✔ OpenPanel Docker container is active."
      else
        echo "✘ OpenPanel Docker container is not active. Writing notification to log file."

        title="OpenPanel docker container is not running. Users are unable to access the OpenPanel interface!"
          error_log=$(docker logs --tail 20 openpanel)
          message="$error_log"
          write_notification "$title" "$message"
      fi
}


mysql_docker_containers_status() {

#### only mysql so far..

      # Check if the MySQL Docker container is running
      if docker ps --format "{{.Names}}" | grep -q "openpanel_mysql"; then
        echo "✔ MySQL Docker container is active."
      else
        echo "✘ MySQL Docker container is not active. Writing notification to log file."

        # Check the last 100 lines of the MySQL error log for the specified condition
        error_log=$(tail -100 /var/log/mysql/error.log | grep -m 1 "No space left on device")
        title="MySQL service is not active. Users are unable to log into OpenPanel!"
        # Check if there's an error log and include it in the message
        if [ -n "$error_log" ]; then
          message="$error_log"
          write_notification "$title" "$message"
        else
          error_log=$(journalctl -n 5 -u "$service_name" 2>/dev/null | sed ':a;N;$!ba;s/\n/\\n/g')
          message="$error_log"
          write_notification "$title" "$message"
        fi
      fi
}


# Function to check service status and write notification if not active
check_service_status() {
  local service_name="$1"
  local title="$2"

  if systemctl is-active --quiet "$service_name"; then
    echo "✔ $service_name is active."
  else
    echo "✘ $service_name is not active. Writing notification to log file."
    local error_log=""

    # example check
    if [ "$service_name" = "example" ]; then
      :
    else
      # For other services, use the existing journalctl command
      error_log=$(journalctl -n 5 -u "$service_name" 2>/dev/null | sed ':a;N;$!ba;s/\n/\\n/g')

      # Check if there's an error log and include it in the message
      if [ -n "$error_log" ]; then
        write_notification "$title" "$error_log"
      else
        echo "no logs."
      fi
    fi
  fi
}


# Function to check system load and write notification if it exceeds the threshold
check_system_load() {
  local title="High System Load!"

  local current_load=$(uptime | awk -F'average:' '{print $2}' | awk -F', ' '{print $1}')
  local load_int=${current_load%.*}  # Extract the integer part
  
  if [ "$load_int" -gt "$LOAD_THRESHOLD" ]; then
    echo "✘ Average Load usage ($load_int) is higher than treshold value ($LOAD_THRESHOLD). Writing notification."
    write_notification "$title" "Current load: $current_load"
  else
    echo "✔ Current Load usage $current_load is lower than the treshold value $LOAD_THRESHOLD. Skipping."
  fi
}



check_swap_usage() {
    local title="SWAP usage alert!"

    # Run the 'free' command and capture the output
    free_output=$(free -t)

    # Extract the used and total swap values
    swap_used=$(echo "$free_output" | awk 'FNR == 3 {print $3}')
    swap_total=$(echo "$free_output" | awk 'FNR == 3 {print $2}')

    # Check if swap_total is greater than 0 to avoid division by zero
    if [ "$swap_total" -gt 0 ]; then
        # Calculate swap usage percentage
        SWAP_USAGE=$(echo "scale=2; $swap_used / $swap_total * 100" | bc)
    else
        # If swap_total is 0, set SWAP_USAGE to 0 or handle it as appropriate
        SWAP_USAGE=0
        echo "✔ Total SWAP is $SWAP_USAGE (skipping swap check for ${SWAP_THRESHOLD}% treshold) - SWAP check was performed at: $TIME "
        return
    fi




    SWAP_USAGE_NO_DECIMALS=$(printf %.0f $SWAP_USAGE)
    
    #Execute check
    if [ "$SWAP_USAGE_NO_DECIMALS" -gt "$SWAP_THRESHOLD" ]; then

      # Check if the lock file exists and is older than 6 hours, then delete it
      if [ -f "$LOCK_FILE" ]; then
          file_age=$(($(date +%s) - $(date -r "$LOCK_FILE" +%s)))
          if [ "$file_age" -gt 21600 ]; then
              echo "Lock file is older than 6 hours. Deleting..."
              rm -f "$LOCK_FILE"
          else
              echo "Previous SWAP cleanup is still in progress. Skipping the current run."
              exit 0
          fi
      fi


        echo "Current SWAP usage ($SWAP_USAGE_NO_DECIMALS) is higher than treshold value ($SWAP_THRESHOLD). Writing notification."        
        write_notification "$title" "Current SWAP usage: $current_load Starting the cleanup process now... you will get a new notification once everything is completed..."
        # create when we start
        touch "$LOCK_FILE"
        
        echo 2 >/proc/sys/vm/drop_caches
        swapoff -a
        swapon -a

        swap_usage=$(free -t | awk 'FNR == 3 {print $3/$2*100}')
        swap_usage_no_decimals=$(printf %.0f $SWAP_USAGE)
        local title_ok="SWAP is cleared - Current value: $swap_usage_no_decimals"
        local title_not_ok="URGENT! SWAP could not be cleared on $HOSTNAME  - Current value: $swap_usage_no_decimals"
        if [ "$swap_usage_no_decimals" -lt "$SWAP_THRESHOLD" ]; then
            echo -e "The Sentinel service has completed clearing SWAP on server $HOSTNAME! \n THANK YOU FOR USING THIS SERVICE! PLEASE REPORT ALL BUGS AND ERRORS TO sentinel@openpanel.co"
            write_notification "$title_ok" "The Sentinel service has completed clearing SWAP on server $HOSTNAME!"
            echo -e "SWAP Usage was abnormal, healing completed, notification sent! This SWAP check was performed at: $TIME"
            # delete after success
            rm -f "$LOCK_FILE"
        else
            echo "✘ URGENT! SWAP could not be cleared on $HOSTNAME"
            write_notification "$title_not_ok" "The Sentinel service detected abnormal SWAP usage at $TIME and tried clearing the space but SWAP usage is still above the $swap_usage_no_decimals treshold."
        fi
    else
        echo "✔ Current SWAP usage is $SWAP_USAGE_NO_DECIMALS (bellow the ${SWAP_THRESHOLD}% treshold) - SWAP check was performed at: $TIME "
        # delete if failed but on next run it is ok
        rm -f "$LOCK_FILE"
    fi
}


# Function to check RAM usage and write notification if it exceeds the threshold
check_ram_usage() {
  local title="High Memory Usage!"

  local total_ram=$(free -m | awk '/^Mem:/{print $2}')
  local used_ram=$(free -m | awk '/^Mem:/{print $3}')
  local ram_percentage=$((used_ram * 100 / total_ram))
  
  local message="Used RAM: $used_ram MB, Total RAM: $total_ram MB, Usage: $ram_percentage%"
  local message_to_check_in_file="Used RAM"

  # Check if there is an unread RAM notification
  if is_unread_message_present "$message_to_check_in_file"; then
    echo "Unread RAM usage notification already exists. Skipping."
    return
  fi

  if [ "$ram_percentage" -gt "$RAM_THRESHOLD" ]; then
    echo "✘ RAM usage ($ram_percentage) is higher than treshold value ($RAM_THRESHOLD). Writing notification."
    write_notification "$title" "$message"
  else
    echo "✔ Current RAM usage $ram_percentage is lower than the treshold value $RAM_THRESHOLD. Skipping."
  fi
}

function check_cpu_usage() {
  local title="High CPU Usage!"

  local cpu_percentage=$(top -bn1 | awk '/^%Cpu/{print $2}' | awk -F'.' '{print $1}')
  
if [ "$cpu_percentage" -gt "$CPU_THRESHOLD" ]; then
  echo "✘ CPU usage ($cpu_percentage) is higher than treshold ($CPU_TRESHOLD). Writing notification."
  top_processes=$(ps aux --sort -%cpu | head -10 | sed ':a;N;$!ba;s/\n/\\n/g')
  write_notification "$title" "CPU Usage: $cpu_percentage% | Top Processes: $top_processes"
else
  echo "✔ Current CPU usage $cpu_percentage is lower than the treshold value $CPU_THRESHOLD. Skipping."
fi
}

function check_disk_usage() {
  local title="Running out of Disk Space!"
  local disk_percentage=$(df -h --output=pcent / | tail -n 1 | tr -d '%')

  if [ "$disk_percentage" -gt "$DISK_THRESHOLD" ]; then

  # Check if there is an unread DU notification
  if is_unread_message_present "$title"; then
    echo "Unread DU notification already exists. Skipping."
    return
  fi
    echo "✘ Disk usage ($disk_percentage) is higher than the treshold value $DISK_THRESHOLD. Writing notification."
    disk_partitions_usage=$(df -h | sort -r -k 5 -i | sed ':a;N;$!ba;s/\n/\\n/g')
    write_notification "$title" "Disk Usage: $disk_percentage% | Partitions: $disk_partitions_usage"
  else
  echo "✔ Current Disk usage $disk_percentage is lower than the treshold value $DISK_THRESHOLD. Skipping."
  fi
}

# Check if --startup flag is present
if [ "$1" == "--startup" ]; then
  perform_startup_action
else
  # Check service statuses and write notifications if needed

  if echo "$SERVICES" | grep -q "nginx"; then
    check_service_status "nginx" "Nginx service is not active. Users' websites are not working!"
  fi

  if echo "$SERVICES" | grep -q "ufw"; then
    check_service_status "ufw" "Firewall (UFW) service is not active. Server and websites are not protected!"
  fi

  if echo "$SERVICES" | grep -q "admin"; then
    check_service_status "admin" "Admin service is not active. OpenAdmin service is not accessible!"
  fi

  if echo "$SERVICES" | grep -q "panel"; then
    panel_docker_containers_status
  fi

  if echo "$SERVICES" | grep -q "docker"; then
    check_service_status "docker" "Docker service is not active. User websites are down!"
  fi

  if echo "$SERVICES" | grep -q "mysql"; then
    mysql_docker_containers_status
    #check_service_status "mysql" "MySQL service is not active. Users are unable to log into OpenPanel!"
  fi

  if echo "$SERVICES" | grep -q "named"; then
    check_service_status "named" "Named (BIND9) service is not active. DNS resolving of domains is not working!"
  fi

  check_new_logins

  check_disk_usage

  check_system_load

  check_ram_usage

  check_cpu_usage

  check_swap_usage

fi
