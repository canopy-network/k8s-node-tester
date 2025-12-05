#!/bin/bash

# current latest version of K3s
K3S_VERSION=${K3S_VERSION:-v1.33.6+k3s1}

# check if running as root, exit otherwise
if [ "$EUID" -ne 0 ]; then
    echo "This script requires root privileges. Please run as root or use sudo."
    exit 1
fi

echo "Installing K3s version $K3S_VERSION"

curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=$K3S_VERSION sh -
echo "K3s installation completed"

echo "Creating local kubectl config"
# get user home directory
HOME=$( getent passwd "$SUDO_USER" | cut -d: -f6 )
echo $HOME
# copy k3s.yaml to user's home directory
mkdir -p $HOME/.kube
cp /etc/rancher/k3s/k3s.yaml $HOME/.kube/config
# set permisions of the created config file for the user who ran sudo
chown -R "$SUDO_USER:$SUDO_USER" $HOME/.kube
chmod 600 $HOME/.kube/config
echo "Local kubectl config created"

echo "K3S setup completed"
echo "-------------------------------------------------------------------------------------------------------------------------"
echo "Server Token: $(cat /var/lib/rancher/k3s/server/node-token)"
echo "-------------------------------------------------------------------------------------------------------------------------"
