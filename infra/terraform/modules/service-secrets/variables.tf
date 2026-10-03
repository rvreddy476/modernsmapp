variable "environment" {
  type = string
}

variable "names" {
  description = "Deployable names; each becomes atpost/<env>/<name>."
  type        = list(string)
}

variable "recovery_window_in_days" {
  description = "Days a deleted secret can be restored. 7 is the minimum; 30 for prod so a mistaken destroy is recoverable for a month."
  type        = number
  default     = 30
}
