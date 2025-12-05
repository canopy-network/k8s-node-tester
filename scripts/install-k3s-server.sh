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

echo "Configuring KUBECONFIG environment variable"
# determine the shell config file
if [ -f "$HOME/.zshrc" ]; then
    SHELL_CONFIG="$HOME/.zshrc"
elif [ -f "$HOME/.bashrc" ]; then
    SHELL_CONFIG="$HOME/.bashrc"
else
    SHELL_CONFIG="$HOME/.profile"
fi

# add KUBECONFIG to shell config if not already present
if ! grep -q "KUBECONFIG=.*/.kube/config" "$SHELL_CONFIG"; then
    echo "export KUBECONFIG=~/.kube/config" >> "$SHELL_CONFIG"
    chown "$SUDO_USER:$SUDO_USER" "$SHELL_CONFIG"
    echo "KUBECONFIG environment variable added to $SHELL_CONFIG"
else
    echo "KUBECONFIG already configured in $SHELL_CONFIG"
fi

echo "K3S setup completed"
TOKEN=$(cat /var/lib/rancher/k3s/server/node-token)
echo "-------------------------------------------------------------------------------------------------------------------------"
echo "Server Token: $TOKEN"
echo "-------------------------------------------------------------------------------------------------------------------------"

echo "To quickly add new nodes, run the following command on the new node:"
echo "curl -sfL https://get.k3s.io | K3S_URL=https://{SERVER_IP}:6443 K3S_TOKEN=$TOKEN sh -"
