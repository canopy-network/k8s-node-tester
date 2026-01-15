#!/bin/bash
set -e

# Hetzner VLAN setup script, based on
# https://docs.hetzner.com/networking/networks/connect-dedi-vswitch/

# configuration from environment variables
SUBNET="${SUBNET:-192.168.100.0/24}"
IP="${IP:-192.168.100.1}"
INTERFACE="${INTERFACE:-enp98s0f0}"
VLAN_ID="${VLAN_ID:-4000}"

# extract network prefix for routing
NETWORK_PREFIX=$(echo $SUBNET | cut -d'.' -f1-2)
GATEWAY=$(echo $IP | sed 's/\.[0-9]*$/\.1/')

echo "Setting up VLAN interface ${INTERFACE}.${VLAN_ID}"
echo "IP: ${IP}"
echo "Subnet: ${SUBNET}"
echo "Gateway: ${GATEWAY}"

# create VLAN interface
ip link add link ${INTERFACE} name ${INTERFACE}.${VLAN_ID} type vlan id ${VLAN_ID}

# set MTU to 1400 as required by Hetzner
ip link set dev ${INTERFACE}.${VLAN_ID} mtu 1400

# bring up the interface
ip link set dev ${INTERFACE}.${VLAN_ID} up

# assign IP address
ip addr add ${IP}/24 dev ${INTERFACE}.${VLAN_ID}

# add route for the network range
ip route add ${NETWORK_PREFIX}.0.0/16 via ${GATEWAY} dev ${INTERFACE}.${VLAN_ID}

echo "VLAN interface configured successfully"
echo "Interface status:"
ip -brief link show ${INTERFACE}.${VLAN_ID}
