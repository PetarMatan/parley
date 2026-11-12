# Testbed targets. Included from the repository Makefile; also usable here.
# Every AWS call goes through the dedicated profile (default: parley).
TESTBED_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
TF := terraform -chdir=$(TESTBED_DIR)terraform

.PHONY: testbed-root-token testbed-seed-gitlab testbed-user-token testbed-admin testbed-init testbed-plan testbed-apply testbed-deploy testbed-status testbed-ssh testbed-stop testbed-start testbed-destroy

testbed-init:
	$(TF) init -input=false

testbed-plan:
	$(TF) plan -input=false

testbed-apply:
	$(TF) apply -input=false

testbed-deploy:
	bash $(TESTBED_DIR)scripts/deploy.sh

testbed-status:
	bash $(TESTBED_DIR)scripts/status.sh

testbed-quarantine:
	bash $(TESTBED_DIR)scripts/quarantine.sh $(IID) $(DIR)

testbed-checkout:
	bash $(TESTBED_DIR)scripts/checkout-setup.sh $(DIR) $(USER)

testbed-admin:
	bash $(TESTBED_DIR)scripts/admin.sh

testbed-ssh:
	bash $(TESTBED_DIR)scripts/ssh.sh

testbed-stop:
	bash $(TESTBED_DIR)scripts/stop.sh

testbed-start:
	bash $(TESTBED_DIR)scripts/start.sh

testbed-destroy:
	bash $(TESTBED_DIR)scripts/destroy.sh

# Phase 3: GitLab seed (testbed/README.md, "Phase 3: GitLab seed").
testbed-root-token:
	@bash $(TESTBED_DIR)scripts/gitlab-root-token.sh > $(TESTBED_DIR).gitlab-root-token && chmod 600 $(TESTBED_DIR).gitlab-root-token && echo "token written to testbed/.gitlab-root-token"

testbed-seed-gitlab:
	bash $(TESTBED_DIR)scripts/seed-gitlab.sh

# A GitLab token for one person's git clone and push: make testbed-user-token USER=bob
testbed-user-token:
	@bash $(TESTBED_DIR)scripts/gitlab-user-token.sh $(USER)
