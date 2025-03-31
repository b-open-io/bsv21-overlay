
module.exports = {
    apps: [
        {
            name: "headers",
            cwd: "../block-headers-service",
            script: "../block-headers-service/server.run",
            args: "-C config.yaml"
        }

    ]
}