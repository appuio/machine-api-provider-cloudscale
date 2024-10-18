{
  apiVersion: 'v1',
  kind: 'Secret',
  metadata: {
    name: 'cloudscale-user-data',
    namespace: 'openshift-machine-api',
  },
  stringData: {
    ignitionHost: std.extVar('ignitionHost'),
    ignitionCA: std.extVar('ignitionCA'),
    userData: (importstr './userdata.jsonnet'),
  },
  type: 'Opaque',
}
