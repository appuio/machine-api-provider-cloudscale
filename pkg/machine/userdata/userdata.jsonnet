local context = std.extVar('context');

// Tries to load user data from a secret specific to the machineset.
// The variable contains `null` if the secret is not found.
// The secret is expected to be named `<machineset>-user-data-managed`.
// The machine set name is read from the machine labels.
local userData =
  local machineSet =
    std.get(
      std.get(context.machine.metadata, 'labels', {}),
      'machine.openshift.io/cluster-api-machineset'
    );
  if machineSet != null then
    local secretName = std.trace("Looking for '%s-user-data-managed'", '%s-user-data-managed' % machineSet);
    local uds = std.filter(function(s) s.metadata.name == secretName, context.secrets);
    if std.length(uds) == 1 && std.objectHas(uds[0].data, 'userData') then
      std.trace("Found user data secret for machineset '%s'",
                machineSet,
                std.parseJson(std.decodeUTF8(std.base64DecodeBytes(uds[0].data.userData))))
    else
      std.trace("No user data secret found for machineset '%s'", machineSet, null)
;

(
  if userData != null then
    userData
  else
    {
      ignition: {
        version: '3.1.0',
        config: {
          merge: [{
            source: 'https://%s:22623/config/%s' % [context.data.ignitionHost, std.get(context.data, 'ignitionConfigName', 'worker')],
          }],
        },
        security: {
          tls: {
            certificateAuthorities: [{
              source: 'data:text/plain;charset=utf-8;base64,%s' % [std.base64(context.data.ignitionCA)],
            }],
          },
        },
      },
    }
) {
  systemd+: {
    units+: [{
      name: 'cloudscale-hostkeys.service',
      enabled: true,
      contents: "[Unit]\nDescription=Print SSH Public Keys to tty\nAfter=sshd-keygen.target\n\n[Install]\nWantedBy=multi-user.target\n\n[Service]\nType=oneshot\nStandardOutput=tty\nTTYPath=/dev/ttyS0\nExecStart=/bin/sh -c \"echo '-----BEGIN SSH HOST KEY KEYS-----'; cat /etc/ssh/ssh_host_*key.pub; echo '-----END SSH HOST KEY KEYS-----'\"",
    }],
  },
  storage+: {
    files+: [{
      filesystem: 'root',
      path: '/etc/hostname',
      mode: 420,
      contents: {
        source: 'data:,%s' % context.machine.metadata.name,
      },
    }],
  },
}
