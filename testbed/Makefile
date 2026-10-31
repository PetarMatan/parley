# Testbed targets. Included from the repository Makefile; also usable here.
# Every AWS call goes through the dedicated profile (default: parley).
TESTBED_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
TF := terraform -chdir=$(TESTBED_DIR)terraform

.PHONY: testbed-init testbed-plan testbed-apply testbed-deploy testbed-status testbed-ssh testbed-stop testbed-start testbed-destroy

testbed-init:
	$(TF) init -input=false

testbed-plan:
	$(TF) plan -input=false

testbed-apply:
	$(TF) apply -input=false

testbed-deploy:
	bash $(TESTBED_DIR)scripts/deploy.sh $(REF)

testbed-status:
	bash $(TESTBED_DIR)scripts/status.sh

testbed-ssh:
	bash $(TESTBED_DIR)scripts/ssh.sh

testbed-stop:
	bash $(TESTBED_DIR)scripts/stop.sh

testbed-start:
	bash $(TESTBED_DIR)scripts/start.sh

testbed-destroy:
	bash $(TESTBED_DIR)scripts/destroy.sh
