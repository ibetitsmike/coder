package codersdk

// ProvisionerTypeSandbox is the provisioner type of the experimental sandbox
// provisioner. Template versions may use it only when the
// fork-sandbox-provisioner experiment is on. ProvisionerTypeValid does not
// accept it yet because no provisioner daemon serves it.
const ProvisionerTypeSandbox ProvisionerType = "sandbox"
