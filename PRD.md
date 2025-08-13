# PRD: BSV21 Overlay Network

## 1. Product overview
### 1.1 Document title and version
- PRD: BSV21 Overlay Network
- Version: 0.0.1

### 1.2 Product summary
The BSV21 Overlay Network is a Go-based server implementation that provides a peer-to-peer overlay network for managing BSV21 fungible tokens on the Bitcoin SV blockchain. This system enables exchanges, applications, and developers to efficiently synchronize and track on-chain token data without requiring full blockchain storage.

The overlay network acts as a specialized indexing and synchronization layer, observing and ingesting token transactions as they occur on-chain. By connecting to a network of peers and subscribing to specific token IDs, users can maintain a complete and up-to-date view of token activity while significantly reducing infrastructure requirements compared to traditional blockchain node operations.

This project serves as critical infrastructure for the growing BSV21 token ecosystem, supporting use cases ranging from cryptocurrency exchanges tracking deposits and withdrawals, to game developers managing in-game assets, to enterprises implementing rewards and loyalty programs. Public instances will be available through nodeless.network, while the open-source nature allows organizations to deploy and operate their own nodes.

## 2. Goals
### 2.1 Business goals
- Provide exchanges an easy way to synchronize on-chain data via overlay P2P network for specific tokens
- Enable efficient BSV21 token management and tracking across diverse industries
- Reduce blockchain data storage and processing requirements for token applications
- Establish foundational infrastructure for the BSV21 token ecosystem
- Support OPL's internal operations and token management needs

### 2.2 User goals
- Quickly integrate BSV21 token support without running full blockchain nodes
- Track specific tokens relevant to their application via simple token ID whitelisting
- Receive real-time updates on token transactions and events
- Query historical token data and current balances efficiently
- Deploy self-hosted instances or connect to public bootstrap nodes

### 2.3 Non-goals
- No wallet functionality or private key management
- No write operations or transaction creation (read-only observation/ingestion)
- No features beyond current implementation scope
- No hosting tier management or rate limiting (handled by nodeless.network)
- No token trading or exchange functionality

## 3. User personas
### 3.1 Key user types
- Exchange developers
- Enterprise token developers
- Game developers
- Individual token creators
- OPL internal team

### 3.2 Basic persona details
- **Exchange Developers**: Technical teams at cryptocurrency exchanges integrating BSV21 token support for deposit/withdrawal processing
- **Enterprise Developers**: Developers across various industries (retail, finance, logistics) implementing tokenized rewards, loyalty points, or asset tracking
- **Game Developers**: Mobile, web, and desktop game developers managing tokenized in-game assets, currencies, and NFT-like items
- **Individual Token Creators**: Independent developers and creators managing personal tokens for communities or projects
- **OPL Internal Team**: Internal operations team using the overlay for company token management and infrastructure

### 3.3 Role-based access
- **Self-Hosted Users**: Full control over their instance configuration, token whitelisting, and peer selection
- **Public Instance Users**: Access determined by nodeless.network policies and configurations
- **Node Operators**: Ability to configure peers, manage synchronization, and maintain overlay network participation

## 4. Functional requirements
- **Token Tracking** (Priority: High)
  - One-to-one mapping between topics and token IDs
  - Selective token synchronization via whitelisting
  - Complete transaction history for tracked tokens
  
- **P2P Synchronization** (Priority: High)
  - Full initial sync on startup with configured peers
  - Continuous live updates via persistent peer connections
  - Auto-reconnection with exponential backoff on connection failures
  
- **Real-time Event Streaming** (Priority: High)
  - Server-sent events (SSE) for live transaction updates
  - Redis pub/sub for internal event distribution
  - Topic-based subscription model using token IDs
  
- **Data Query Capabilities** (Priority: High)
  - Token transaction history lookup
  - Current token balance queries
  - UTXO validation and tracking
  
- **Standard Overlay Protocol** (Priority: High)
  - Submit endpoint for transaction ingestion
  - Lookup endpoint for data queries
  - Sync endpoints for peer communication
  - Subscribe endpoint for SSE streams

## 5. User experience
### 5.1. Entry points & first-time user flow
- Discovery through nodeless.network showcase, GitHub repository, or technical partner referral
- Clone repository from GitHub or deploy via nodeless.network
- Configure environment variables (Redis connection, block headers API, peers)
- Add desired token IDs to whitelist for tracking
- Start server to initiate peer synchronization

### 5.2. Core experience
- **Initial Setup**: Configure Redis storage and add token IDs to the whitelist for selective tracking
  - Simple token ID-based configuration makes onboarding straightforward
- **Peer Configuration**: Set up connections to peers supporting the same token IDs
  - Bootstrap nodes provided for easy initial connectivity
- **Synchronization**: System performs full sync on startup followed by continuous live updates
  - Progress monitoring ensures users know sync status
- **API Integration**: Connect applications using standard RESTful endpoints and SSE streams
  - Well-documented API makes integration predictable
- **Monitoring**: Track synchronization status, peer connections, and event processing
  - Clear logging and status endpoints provide operational visibility

### 5.3. Advanced features & edge cases
- Graceful shutdown with proper connection cleanup and state persistence
- Multiple storage backend support (Redis primary, SQLite alternative)
- Buffered channels preventing subscription overflow
- Automatic peer discovery via SHIP protocol (future enhancement)
- Transaction validation via merkle proof ingestion

### 5.4. UI/UX highlights
- RESTful API design following overlay protocol standards
- Simple token ID-based subscription model
- Real-time streaming via standard SSE protocol
- JSON-based request/response formats
- Comprehensive error messages and status codes

## 6. Narrative
Sarah is a backend developer at a mid-sized cryptocurrency exchange that wants to add support for BSV21 tokens. After discussing integration options with a BSV ecosystem partner, she discovers the overlay network project on nodeless.network. Sarah clones the repository and reviews the documentation, finding the setup surprisingly straightforward. She configures the server with her exchange's Redis instance and adds the token IDs for the three BSV21 tokens they plan to support. Upon starting the server, it automatically connects to public bootstrap nodes and begins synchronizing all historical transactions for those tokens. Within 30 minutes, Sarah's exchange has a complete view of all token transactions and balances. She integrates the SSE endpoint into their deposit monitoring system, and the exchange can now reliably credit customer deposits and process withdrawals for BSV21 tokens without running a full BSV node, saving significant infrastructure costs while maintaining security and reliability.

## 7. Success metrics
### 7.1. User-centric metrics
- Number of GitHub stars and forks
- Active overlay network nodes reported
- Developer adoption rate and community growth
- Time to first successful token synchronization
- API integration success rate

### 7.2. Business metrics
- Number of exchanges successfully using the system
- Total number of applications integrated
- Volume of tokens tracked across the network
- Number of self-hosted deployments
- Public instance utilization rates

### 7.3. Technical metrics
- API response time under 100ms for queries
- Ingestion rate of 1000+ transactions per second
- Initial synchronization completing within 1 hour for typical tokens
- 99.9% uptime for public instances
- Peer connection stability and reconnection success rate

## 8. Technical considerations
### 8.1. Integration points
- Redis for high-performance storage and pub/sub messaging
- ARC API for transaction broadcasting to the network
- JungleBus for blockchain data synchronization
- Block Headers API for chain validation and tracking
- SHIP protocol for peer discovery (planned)
- Standard overlay protocol for peer communication

### 8.2. Data storage & privacy
- Redis as primary storage for event data and caching
- SQLite as alternative storage backend option
- No personal data storage beyond public blockchain data
- Token transaction data is public by nature
- Configuration data stored in environment variables

### 8.3. Scalability & performance
- Horizontal scaling through multiple peer nodes
- Buffered channels preventing subscription overflow
- Efficient binary BEEF format for transaction data
- Selective token synchronization reducing data requirements
- P2P architecture distributing network load

### 8.4. Potential challenges
- P2P synchronization guarantees inherently difficult due to distributed nature
- Initial synchronization optimization for tokens with long history
- Balancing download speed vs ingestion processing performance
- Peer discovery and automatic configuration improvements needed
- Network partition handling and conflict resolution

## 9. Milestones & sequencing
### 9.1. Project estimate
- Small: 2-4 weeks for MVP delivery

### 9.2. Team size & composition
- Small Team: 5 total people
  - Luke and Dave (Engineers)
  - Dan (Project Management)
  - Michael (DevOps)
  - Kurt (Client Account Manager)

### 9.3. Suggested phases
- **Phase 1**: MVP Delivery (2 weeks)
  - Complete core functionality refinement
  - Test with exchange partners
  - Deploy initial public instances
  - Ensure production readiness
  
- **Phase 2**: Documentation Review (1 week)
  - Review and update existing documentation
  - Create deployment guides
  - Publish API reference
  - Add troubleshooting guides
  
- **Phase 3**: Exchange Deployment (1 week)
  - Support initial exchange integrations
  - Monitor performance and stability
  - Address immediate feedback
  - Optimize based on real usage
  
- **Phase 4**: Peer Discovery Enhancement (Post-MVP)
  - Implement SHIP protocol integration
  - Automatic peer discovery
  - Dynamic peer configuration
  - Network topology optimization

## 10. User stories
### 10.1. Track token transactions
- **ID**: US-001
- **Description**: As an exchange developer, I want to track all BSV21 token transactions for specific tokens so that I can credit user deposits accurately.
- **Acceptance criteria**:
  - The system synchronizes all historical transactions for whitelisted tokens
  - New transactions are received within seconds of blockchain confirmation
  - Transaction data includes all necessary fields for processing deposits
  - Failed transactions are properly identified and handled

### 10.2. Subscribe to real-time events
- **ID**: US-002
- **Description**: As a developer, I want to subscribe to real-time token events so that my application stays synchronized with on-chain activity.
- **Acceptance criteria**:
  - SSE endpoint provides real-time transaction updates
  - Events are delivered for all whitelisted tokens
  - Connection automatically reconnects on failure
  - Events include transaction ID, token ID, and amount data

### 10.3. Synchronize with network peers
- **ID**: US-003
- **Description**: As a node operator, I want to synchronize with network peers so that I maintain complete token history.
- **Acceptance criteria**:
  - Full synchronization occurs on startup
  - Continuous updates received from connected peers
  - Synchronization state is persisted across restarts
  - Progress indicators show sync status

### 10.4. Query token balances
- **ID**: US-004
- **Description**: As an API consumer, I want to query current token balances so that I can display accurate information in my application.
- **Acceptance criteria**:
  - Balance queries return current UTXO set for a token
  - Response includes balance amount and transaction count
  - Queries complete within 100ms for typical requests
  - Results are consistent with latest synchronized state

### 10.5. Query transaction history
- **ID**: US-005
- **Description**: As a developer, I want to query token transaction history so that I can audit and display historical activity.
- **Acceptance criteria**:
  - History queries support pagination for large result sets
  - Transactions include timestamp, amount, and participant data
  - Filtering by date range is supported
  - Results are returned in chronological order

### 10.6. Configure token whitelist
- **ID**: US-006
- **Description**: As a node operator, I want to configure which tokens to track so that I only synchronize relevant data.
- **Acceptance criteria**:
  - Token IDs can be added to whitelist via configuration
  - Only whitelisted tokens are synchronized
  - Configuration changes take effect on restart
  - Invalid token IDs are logged and skipped

### 10.7. Monitor synchronization status
- **ID**: US-007
- **Description**: As a system administrator, I want to monitor synchronization status so that I can ensure the system is operating correctly.
- **Acceptance criteria**:
  - Status endpoint shows peer connection states
  - Synchronization progress is reported
  - Error conditions are clearly indicated
  - Metrics are exposed for monitoring systems

### 10.8. Handle connection failures
- **ID**: US-008
- **Description**: As a node operator, I want the system to handle connection failures gracefully so that temporary network issues don't cause data loss.
- **Acceptance criteria**:
  - Disconnected peers trigger automatic reconnection
  - Exponential backoff prevents connection flooding
  - Synchronization resumes from last known state
  - Connection failures are logged appropriately

### 10.9. Validate transaction proofs
- **ID**: US-009
- **Description**: As an exchange, I want to validate transaction merkle proofs so that I can ensure transaction finality.
- **Acceptance criteria**:
  - ARC webhook ingests merkle proof data
  - Proofs are validated against block headers
  - Validated transactions update confirmation status
  - Invalid proofs are rejected and logged

### 10.10. Deploy via Docker
- **ID**: US-010
- **Description**: As a DevOps engineer, I want to deploy the overlay server via Docker so that I can maintain consistent environments.
- **Acceptance criteria**:
  - Docker image builds successfully
  - Environment variables configure the container
  - Container starts and connects to peers
  - Health checks indicate operational status