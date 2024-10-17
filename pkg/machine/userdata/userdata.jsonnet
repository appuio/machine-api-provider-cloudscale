local context = std.extVar('context');

{
  ignition: {
    version: '3.1.0',
    config: {
      merge: [ {
        source: 'https://%s:22623/config/%s' % [ context.data.ignitionHost, std.get(context.data, 'ignitionConfigName', 'worker') ],
      } ],
    },
    security: {
      tls: {
        certificateAuthorities: [ {
          source: 'data:text/plain;charset=utf-8;base64,%s' % [ std.base64(context.data.ignitionCA) ],
        } ],
      },
    },
  },
  systemd: {
    units: [ {
      name: 'cloudscale-hostkeys.service',
      enabled: true,
      contents: "[Unit]\nDescription=Print SSH Public Keys to tty\nAfter=sshd-keygen.target\n\n[Install]\nWantedBy=multi-user.target\n\n[Service]\nType=oneshot\nStandardOutput=tty\nTTYPath=/dev/ttyS0\nExecStart=/bin/sh -c \"echo '-----BEGIN SSH HOST KEY KEYS-----'; cat /etc/ssh/ssh_host_*key.pub; echo '-----END SSH HOST KEY KEYS-----'\"",
    } ],
  },
  storage: {
    files: [ {
      filesystem: 'root',
      path: '/etc/hostname',
      mode: 420,
      contents: {
        source: 'data:,%s' % context.machine.metadata.name,
      },
    } ],
  },
}
