# Gateway

A Gateway facilitates the interactions between the local node and remote nodes
(peers). It relays incoming blocks and transactions to local modules, and
broadcasts outgoing blocks and transactions to peers. In a broad sense, it is
responsible for ensuring that the local consensus set is consistent with the
"network" consensus set.

The gateway connects a Sia node to the Sia flood network. The flood network is
used to propagate blocks and transactions. The gateway is the primary avenue
that a node uses to hear about transactions and blocks, and is the primary
avenue used to tell the network about blocks that you have mined or about
transactions that you have created.

For the user to be securely connected to the network, the user must be connected
to at least one node which will send them all of the blocks. An attacker can
trick the user into thinking that a different blockchain is the full blockchain
if the user is not connected to any nodes who are seeing + broadcasting the real
chain (and instead is connected only to attacker nodes or to nodes that are not
broadcasting). This situation is called an eclipse attack.

Connecting to a large number of nodes increases the resiliency of the network,
but also puts a networking burden on the nodes and can slow down block
propagation or increase orphan rates. The gateway's job is to keep the network
efficient while also protecting the user against attacks.

The gateway keeps a list of nodes that it knows about. It uses this list to form
connections with other nodes, and then uses those connections to participate in
the flood network. The primary vector for an attacker to achieve an eclipse
attack is node list domination. If a gateway's nodelist is heavily dominated by
attacking nodes, then when the gateway chooses to make random connections the
gateway is at risk of selecting only attacker nodes.

Some research has been done on Bitcoin's flood networks. The more relevant
research has been listed below. The papers listed first are more relevant.
    Eclipse Attacks on Bitcoin's Peer-to-Peer Network (Heilman, Kendler, Zohar, Goldberg)
    Stubborn Mining: Generalizing Selfish Mining and Combining with an Eclipse Attack (Nayak, Kumar, Miller, Shi)
    An Overview of BGP Hijacking (https://www.bishopfox.com/blog/2015/08/an-overview-of-bgp-hijacking/)

## Alerts
The gateway might register the following alerts:

- *Gateway-Offline*: Registered when the gateway is not connected to the public internet

**TODO**
 - Fill in subsystems