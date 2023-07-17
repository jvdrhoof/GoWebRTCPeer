# GoWebRTCPeer
 
## Usage

### Specifiy Proxy Port

Use -p followed by the port ex: ./peer.exe -p :8000

The ":" is important, it wont work otherwise.


### Specify Remote Input

Use -i in addition to -p to receive remote frames from the plugin and use these as a frame source. When not using this option the application will fallback to using preloaded files.

### Specift Remote Output

Use -o in addition to -p to forward the received packets to the plugin, allowing you to process the frames in the application using the plugin.
