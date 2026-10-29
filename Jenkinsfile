node(label: 'master') {
    // ============================================
    // GitOps CI Pipeline for materm-web-portal-backend
    // ============================================
    // This pipeline follows GitOps workflow:
    // 1. Build & push Docker image
    // 2. Update image tag in GitOps repository
    // 3. ArgoCD automatically syncs the deployment
    // ============================================

    def SERVICE_NAME
    def SERVICE_DIR
    def BRANCH_NAME
    def VERSION
    def PORT = 3000
    def TARGET_ENV
    def DOCKER_TAG
    def COMMIT_SHA
    def COMMIT_AUTHOR_NAME
    def COMMIT_MESSAGE
    def GITOPS_BRANCH
    def NODEPORT
    def TEMPLATE_DIR = env.TEMPLATE_DIR ?: 'templates'

    // GitOps Configuration
    def GITOPS_REPO = env.GITOPS_REPO
    def GITOPS_CREDENTIALS_ID = env.GITOPS_CREDENTIALS_ID
    def GITOPS_USER_EMAIL = env.GITOPS_USER_EMAIL
    def GITOPS_USER_NAME = env.GITOPS_USER_NAME

    try {
        // ============================================
        // STEP 1: Checkout SCM & Determine Environment.  
        // ============================================
        stage('STEP1: Checkout SCM') {
            println "Running Stage: Checkout SCM"
            deleteDir() // delete workspace
            checkout scm
            // Add application repo path
            SERVICE_NAME = scm.getUserRemoteConfigs()[0].getUrl().tokenize('/').last().split("\\.")[0]
            BRANCH_NAME = env.BRANCH_NAME
            SERVICE_DIR = SERVICE_NAME.split('-')[0]  // Extract first part: "materm-web-portal-backend" -> "materm"
            COMMIT_SHA = sh(script: 'git rev-parse --short HEAD', returnStdout: true).trim()

            def lines = readFile('version').trim().split('\n')

            PORT = 3000
            VERSION = lines[0].trim()
            NODEPORT = lines[1].trim()

            println "SERVICE_NAME: ${SERVICE_NAME}"
            println "SERVICE_DIR: ${SERVICE_DIR}"
            println "BRANCH_NAME: ${BRANCH_NAME}"
            println "VERSION: ${VERSION}"
            println "PORT_APP: ${PORT}"
            println "NODEPORT: ${NODEPORT}"
            println "COMMIT_SHA: ${COMMIT_SHA}"
            
            // Only deploy from branches whose names start with test, dev, or uat
            TARGET_ENV = ''
            GITOPS_BRANCH = ''
            
            if (BRANCH_NAME.startsWith('test')) {
                TARGET_ENV = 'test'
                GITOPS_BRANCH = 'test'
                DOCKER_TAG = "${VERSION}-${TARGET_ENV}-${COMMIT_SHA}"
            } else if (BRANCH_NAME.startsWith('dev')) {
                TARGET_ENV = 'dev'
                GITOPS_BRANCH = 'dev'
                DOCKER_TAG = "${VERSION}-${TARGET_ENV}-${COMMIT_SHA}"
            } else if (BRANCH_NAME == 'main' || BRANCH_NAME == 'master') {
                TARGET_ENV = 'prod'
                GITOPS_BRANCH = 'main'
                DOCKER_TAG = "${VERSION}"
            } else if (BRANCH_NAME.startsWith('uat')) {
                TARGET_ENV = 'uat'
                GITOPS_BRANCH = 'uat'
                DOCKER_TAG = "${VERSION}-${TARGET_ENV}-${COMMIT_SHA}"
            } else {
                println "⏭️ Skipping deployment - Branch '${BRANCH_NAME}' is not configured for deployment"
                println "   Allowed: branch name starts with test, dev, or uat (or main for prod)."
                currentBuild.result = 'SUCCESS'
                return
            }
        }

        // Match actual TARGET_ENV values (was wrongly using 'main' while STEP1 sets 'prod' for main)
        if (TARGET_ENV in ['test', 'dev', 'uat', 'prod']) {
            // ============================================
            // STEP 2: Get Git Commit Information
            // ============================================
            stage('STEP2: Get Git Information') {
                echo "Running Stage: Get Commit Information"
                def commitInfo = sh(script: 'git log -n 1 --pretty=format:"%an %ae %s"', returnStdout: true).trim().split(' ', 3)
                COMMIT_AUTHOR_NAME = commitInfo[0]
                COMMIT_MESSAGE = commitInfo[2]
                
            }

            // ============================================
            // STEP 3: Build & Push Docker Image
            // ============================================
            stage('STEP3: Build & Push Docker Image') {
                println "Running Stage: Build & Push Docker Image"
                def DOCKER_HUB_USERNAME = env.DOCKER_HUB_USERNAME
                def DOCKER_IMAGE = "${DOCKER_HUB_USERNAME}/${SERVICE_NAME}"
                def IMAGE_WITH_TAG = "${DOCKER_IMAGE}:${DOCKER_TAG}"
                def REGISTRY = env.DOCKER_REGISTRY_URL

                println "Building Docker Image (Docker Pipeline): ${IMAGE_WITH_TAG}"

                withEnv(["DOCKER_DEFAULT_PLATFORM=linux/amd64", "DOCKER_BUILDKIT=1"]) {
                    docker.withRegistry(REGISTRY, env.DOCKER_CREDENTIALS_ID) {
                        def image = docker.build(IMAGE_WITH_TAG, "-f Dockerfile .")
                        image.push()
                    }
                }
            }

// ============================================
// STEP 4: Update GitOps Repository (GitOps push only for branches test, dev, uat)
// ============================================
            stage('STEP4: Update GitOps Repository') {
                if (!(TARGET_ENV in ['test', 'dev', 'uat'])) {
                    println "⏭️ Skipping STEP4 (GitOps) — only runs for TARGET_ENV test, dev, uat (current: ${TARGET_ENV})"
                } else {
                    println "Running Stage: Update GitOps Repository"

                    def DOCKER_HUB_USERNAME = env.DOCKER_HUB_USERNAME
                    def DOCKER_IMAGE = "${DOCKER_HUB_USERNAME}/${SERVICE_NAME}"

                    script {
                        dir('gitops') {
                            git(
                                url: GITOPS_REPO,
                                credentialsId: GITOPS_CREDENTIALS_ID,
                                branch: GITOPS_BRANCH
                            )

                            println "✅ Cloned GitOps repo on branch: ${GITOPS_BRANCH}"

                            sh """
                                git config user.email "${GITOPS_USER_EMAIL}"
                                git config user.name "${GITOPS_USER_NAME}"
                            """

                            sh "./main.sh \"${SERVICE_NAME}\" \"${DOCKER_IMAGE}\" \"${DOCKER_TAG}\" \"${TARGET_ENV}\" \"${GITOPS_REPO}\" \"${GITOPS_BRANCH}\" \"${TEMPLATE_DIR}\" \"${BUILD_NUMBER}\" \"${NODEPORT}\""

                            println "🚀 Pushing changes to ${GITOPS_BRANCH}..."

                            withCredentials([usernamePassword(
                                credentialsId: GITOPS_CREDENTIALS_ID,
                                usernameVariable: 'GIT_USERNAME',
                                passwordVariable: 'GIT_PASSWORD'
                            )]) {
                                def gitRepoUrl = GITOPS_REPO.replace('https://', '')
                                sh """
                                    git push https://\${GIT_USERNAME}:\${GIT_PASSWORD}@${gitRepoUrl} ${GITOPS_BRANCH}
                                """
                            }

                            println "✅ GitOps repository updated successfully!"
                            println "📍 Branch: ${GITOPS_BRANCH}"
                            println "🏷️  Image: ${DOCKER_IMAGE}:${DOCKER_TAG}"
                            println "🚀 ArgoCD will automatically sync changes to ${TARGET_ENV} environment"
                        }
                    }
                }
            }
            // ============================================
            // STEP 5: Notify Deployment Status
            // ============================================
            stage('STEP5: Pipeline Notification') {
                println "🔕 Notifications are disabled (STEP5 skipped)."
            }
        } else {
            println "⏭️ Skipping deployment — TARGET_ENV is empty or not in deploy set (branch: '${BRANCH_NAME}', TARGET_ENV: '${TARGET_ENV}')."
            println "   Deploy runs for TARGET_ENV in: test, dev, uat, prod (after normalizing e.g. origin/test → test)."
        }

    } catch (err) {
        stage('FAILURE: Pipeline Alert') {
            build job: 'TODO-materm-failure-notification-job', // TODO: set Materm's Jenkins notification job path (the comma used to sit inside this comment, so `parameters` was never passed)
            parameters: [
                string(name: 'NAME', value: "\n\nJob:\nContinuous Integration (CI)\n\nService Name: \n${SERVICE_NAME} \n\nVersion: \n${DOCKER_TAG} \n\nDeployer: \n${COMMIT_AUTHOR_NAME} \n\nError: \n${err}\n\n"),
                string(name: 'STATUS', value: "FAILURE"),
            ]
        }
        currentBuild.result = 'FAILURE'
    }
}
