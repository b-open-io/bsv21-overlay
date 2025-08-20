import React, { useState, useEffect } from 'react';
import './App.css';
import {
    GetWhitelistedTokens,
    AddTokenToWhitelist,
    RemoveTokenFromWhitelist,
    GetTopicPeerConfig,
    SetTopicPeerConfig
} from "../wailsjs/go/main/App";
import { config } from "../wailsjs/go/models";

type TopicPeerConfig = config.TopicPeerConfig;

function App() {
    const [whitelistedTokens, setWhitelistedTokens] = useState<string[]>([]);
    const [newTokenId, setNewTokenId] = useState('');
    const [selectedToken, setSelectedToken] = useState<string>('');
    const [topicConfig, setTopicConfig] = useState<TopicPeerConfig | null>(null);
    const [status, setStatus] = useState('');

    // Load initial data
    useEffect(() => {
        loadWhitelistedTokens();
    }, []);

    // Load topic config when token is selected
    useEffect(() => {
        if (selectedToken) {
            loadTopicConfig(selectedToken);
        } else {
            setTopicConfig(null);
        }
    }, [selectedToken]);

    const loadWhitelistedTokens = async () => {
        try {
            const tokens = await GetWhitelistedTokens();
            setWhitelistedTokens(tokens || []);
        } catch (error) {
            setStatus(`Error loading tokens: ${error}`);
        }
    };


    const loadTopicConfig = async (tokenId: string) => {
        try {
            const config = await GetTopicPeerConfig(tokenId);
            setTopicConfig(config);
        } catch (error) {
            setStatus(`Error loading topic config: ${error}`);
        }
    };

    const handleAddToken = async () => {
        if (!newTokenId.trim()) return;
        
        try {
            await AddTokenToWhitelist(newTokenId.trim());
            setNewTokenId('');
            await loadWhitelistedTokens();
            setStatus(`Added token: ${newTokenId}`);
        } catch (error) {
            setStatus(`Error adding token: ${error}`);
        }
    };

    const handleRemoveToken = async (tokenId: string) => {
        try {
            await RemoveTokenFromWhitelist(tokenId);
            await loadWhitelistedTokens();
            if (selectedToken === tokenId) {
                setSelectedToken('');
                setTopicConfig(null);
            }
            setStatus(`Removed token: ${tokenId}`);
        } catch (error) {
            setStatus(`Error removing token: ${error}`);
        }
    };


    const handleUpdateTopicConfig = async () => {
        if (!topicConfig || !selectedToken) return;
        
        try {
            await SetTopicPeerConfig(selectedToken, topicConfig);
            setStatus(`Topic configuration updated for ${selectedToken}`);
        } catch (error) {
            setStatus(`Error updating topic config: ${error}`);
        }
    };

    return (
        <div className="app">
            <h1>BSV21 Overlay Configuration</h1>
            
            {status && (
                <div className="status-bar">
                    {status}
                </div>
            )}

            <div className="config-sections">
                {/* Token Whitelist Section */}
                <section className="config-section">
                    <h2>Token Whitelist</h2>
                    
                    <div className="add-token">
                        <input
                            type="text"
                            placeholder="Enter token ID"
                            value={newTokenId}
                            onChange={(e) => setNewTokenId(e.target.value)}
                            onKeyPress={(e) => e.key === 'Enter' && handleAddToken()}
                        />
                        <button onClick={handleAddToken}>Add Token</button>
                    </div>

                    <div className="token-list">
                        {whitelistedTokens.length === 0 ? (
                            <p>No tokens whitelisted</p>
                        ) : (
                            whitelistedTokens.map(token => (
                                <div key={token} className="token-item">
                                    <span 
                                        className={`token-id ${selectedToken === token ? 'selected' : ''}`}
                                        onClick={() => setSelectedToken(token)}
                                    >
                                        {token}
                                    </span>
                                    <button 
                                        onClick={() => handleRemoveToken(token)}
                                        className="remove-btn"
                                    >
                                        Remove
                                    </button>
                                </div>
                            ))
                        )}
                    </div>
                </section>

                {/* Placeholder for future sections */}
                <section className="config-section">
                    <h2>Instructions</h2>
                    <p>Select a token from the whitelist to configure its peer settings.</p>
                    <p>Each peer can have individual SSE, GASP, and broadcast configurations.</p>
                </section>

                {/* Topic-Specific Configuration */}
                {selectedToken && topicConfig && (
                    <section className="config-section">
                        <h2>Topic Configuration: {selectedToken}</h2>
                        
                        <div className="config-form">
                            <div className="form-group">
                                <label>Peers:</label>
                                
                                {Object.entries(topicConfig.peers).map(([peerURL, peerSettings], index) => (
                                    <div key={index} className="peer-config">
                                        <div className="peer-url">
                                            <input
                                                type="text"
                                                placeholder="Peer URL"
                                                value={peerURL}
                                                onChange={(e) => {
                                                    const newURL = e.target.value;
                                                    const updatedPeers = { ...topicConfig.peers };
                                                    delete updatedPeers[peerURL];
                                                    updatedPeers[newURL] = peerSettings;
                                                    setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                                }}
                                            />
                                            <button 
                                                onClick={() => {
                                                    const updatedPeers = { ...topicConfig.peers };
                                                    delete updatedPeers[peerURL];
                                                    setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                                }}
                                                className="remove-peer-btn"
                                            >
                                                Remove
                                            </button>
                                        </div>
                                        
                                        <div className="peer-settings">
                                            <label>
                                                <input
                                                    type="checkbox"
                                                    checked={peerSettings.sse}
                                                    onChange={(e) => {
                                                        const updatedPeers = { ...topicConfig.peers };
                                                        updatedPeers[peerURL] = new config.PeerSettings({ ...peerSettings, sse: e.target.checked });
                                                        setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                                    }}
                                                />
                                                SSE
                                            </label>
                                            
                                            <label>
                                                <input
                                                    type="checkbox"
                                                    checked={peerSettings.gasp}
                                                    onChange={(e) => {
                                                        const updatedPeers = { ...topicConfig.peers };
                                                        updatedPeers[peerURL] = new config.PeerSettings({ ...peerSettings, gasp: e.target.checked });
                                                        setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                                    }}
                                                />
                                                GASP
                                            </label>
                                            
                                            <label>
                                                <input
                                                    type="checkbox"
                                                    checked={peerSettings.broadcast}
                                                    onChange={(e) => {
                                                        const updatedPeers = { ...topicConfig.peers };
                                                        updatedPeers[peerURL] = new config.PeerSettings({ ...peerSettings, broadcast: e.target.checked });
                                                        setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                                    }}
                                                />
                                                Broadcast
                                            </label>
                                        </div>
                                    </div>
                                ))}
                                
                                <button 
                                    onClick={() => {
                                        const newPeerURL = 'https://';
                                        const newPeerSettings = new config.PeerSettings({ sse: false, gasp: false, broadcast: false });
                                        const updatedPeers = { ...topicConfig.peers, [newPeerURL]: newPeerSettings };
                                        setTopicConfig(new config.TopicPeerConfig({ peers: updatedPeers }));
                                    }}
                                    className="add-peer-btn"
                                >
                                    Add Peer
                                </button>
                            </div>

                            <button onClick={handleUpdateTopicConfig}>Update Topic Config</button>
                        </div>
                    </section>
                )}
            </div>
        </div>
    );
}

export default App;
