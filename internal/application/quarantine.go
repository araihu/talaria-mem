package application

// QuarantineStore and the read-time quarantine protocol live in
// content_output.go. Keeping the protocol behind a narrow port prevents
// output adapters from inventing a second scanner boundary.
