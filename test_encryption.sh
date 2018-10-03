#!/usr/bin/env bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.


sudo chmod 777 /run/containerd/containerd.sock

CTR=${CTR:-./bin/ctr}
SLEEP_TIME=${SLEEP_TIME:-2.0}

ALPINE=docker.io/library/alpine:latest
ALPINE_ENC=docker.io/library/alpine:enc
ALPINE_DEC=docker.io/library/alpine:dec
ALPINE_ENC_EXPORT_NAME=alpine.enc
ALPINE_ENC_IMPORT_BASE=docker.io/library/alpine

NGINX=docker.io/library/nginx:latest
NGINX_ENC=docker.io/library/nginx:enc
NGINX_DEC=docker.io/library/nginx:dec

# gpg2 --export-secret-key ...
GPGTESTKEY1="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"
GPGTESTKEY2="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"

JWKTESTKEY1='{"d":"pGBXnqbPbMR6PIPkyzz1OhmJq4ORmlHwh2GunXJuzSj1AhYL9rZ8fd_NNn128yPmllTN6LOBqNj1vqXOnMaeu63vdn08z8xTDlCsuUt2T0NzgQlPuducu8K0OURFqf-C3dIPqipxnWKydN7_gYEEYosxgKU3B8WolA65YFTaUxv-NQL-3rASUTtiQ1rtm2l-RBEIqOuFh350Bahnq_gtINxKpVahpLDiLTte6HpnbzU7ei_dW4v3j6foMg2pOWUAcfxNfmZwQO-eEge88E5WfN7HIQnBTTjAjrNwIP-SfaDmKpa37at1kTG932If0VopQ9CJZE_jM2wHx3VfZiTmAQ","dp":"RFUZdzAaCs3ak4lxptnHy5J_ujWgHk1CvzyIU1tEw1P9BCme-pW30YdEvXkXMzqiX8g0p6WdEvbfx0I9dctje9IbjCQcemxjIUx-2ifUppp8_I4BCaZ4K4puyt65TJL2za6PmyuVTDlugYceMIupmZ4bx6C70bjTeo1ErVe-yYE","dq":"zOCBbLcqCtkXUQqlmOEmb35GBc5HLV6LcQSYAm1mhMIRjK-cSiXAlg4yKhXoGNAuU-LBXyVLeOa4cNdG_v-34XZGmqIyBWG1ehmMumcblzI2-Cuj76jW26sWBvPBH7cyEf1FULS3acF-xPd8TkNA9P0laZmCshOfa_-zkMM5Tf8","e":"AQAB","kty":"RSA","n":"zMwIcZn24y3Aj-P5Vox-w54FkpoRGeYGhyF7rdDQN2bYO-8h09doVbbgstYauyZKRvk3iWoOwfY9foD0hHCJNtT20sqbx40osGN9qLERweO6Xn8adhVPN7isTT9KozdvsrOIBr7uQUsruvow4klIYrv5FqS_RHpy4f0CUlsjPqc3F5PC4yV0D0f_QUApr06--uHRdH3ucunvdwR1V1IZV0DEJwZ5DzEDQmynzo5oV1UVNb9DSzTXsUAzSipCrdIyUxCnofPp_PzKvqMbctBAchx0AKN8IK8Z3RGFYyrV3HxkXqFxZ4aTVnkXqlnGV5CRQhx59ckIWUxAlyLcGLXvmw","p":"_aCnqjENQBE-he_7XWBo7kXJHnOz6SucuLNPo35imTO4nJBkga9HOF8VxeM3OrskEFVudkDvSqbq4KtERiCGL8f3-LAUKSFaxULa0h9FPJOlks_JXVlDwGsXOyHirIHIEvvbjAAQlV_F7tQNCzSHuXmegh3yJWLwz6EcUw2z9YE","q":"zrZyXsm2jVHc9JkWEp8CMJ0J65f87KrYjQgcb46XkCK1E7bnFDLiNzYV-CQ8a9kKuWfd_LUx2FIjwrik5IFQXJA7Z7s4jvAh2J-pLutSD4sU0KAXcH8W85jLd9C0varGXWFFD7axv-FjDEEQ8TL35Nh5svILn_hgMfB2TPNuixs","qi":"GgGk6GPOtfo2TFtuPQPVTTPGmEzoVekZNH9VQfvQchiRyU1cddYWGRzzJct1zP0GhRsam7m27zguxxVVOORjM5NAPHhjhuwmncmi5hZDyfyIURPXOgslPNG42XdIZdfJtgxqUuOhLNfeQcQXJM8S2EpauLmlm14blP5V-7ZOXO0"}'

JWKTESTPUBKEY1='{"e":"AQAB","kty":"RSA","n":"zMwIcZn24y3Aj-P5Vox-w54FkpoRGeYGhyF7rdDQN2bYO-8h09doVbbgstYauyZKRvk3iWoOwfY9foD0hHCJNtT20sqbx40osGN9qLERweO6Xn8adhVPN7isTT9KozdvsrOIBr7uQUsruvow4klIYrv5FqS_RHpy4f0CUlsjPqc3F5PC4yV0D0f_QUApr06--uHRdH3ucunvdwR1V1IZV0DEJwZ5DzEDQmynzo5oV1UVNb9DSzTXsUAzSipCrdIyUxCnofPp_PzKvqMbctBAchx0AKN8IK8Z3RGFYyrV3HxkXqFxZ4aTVnkXqlnGV5CRQhx59ckIWUxAlyLcGLXvmw"}'

trap "cleanup" EXIT QUIT

cleanup() {
	if [ -n "${WORKDIR}" ]; then
		rm -rf ${WORKDIR}
	fi
}
WORKDIR=$(mktemp -d)

failExit() {
	local rc=$1
	local msg="$2"

	if [ $rc -ne 0 ]; then
		echo -e "Error: $msg" >&2
		echo >&2
		exit 1
	fi
}

$CTR images rm --sync ${ALPINE_ENC} ${ALPINE_DEC} ${NGINX_ENC} ${NGINX_DEC} &>/dev/null
$CTR images pull --all-platforms ${ALPINE} &>/dev/null
failExit $? "Image pull failed on ${ALPINE}"

$CTR images pull --platform linux/amd64 ${NGINX} &>/dev/null
failExit $? "Image pull failed on ${NGINX}"

LAYER_INFO_ALPINE="$($CTR images layerinfo ${ALPINE})"
failExit $? "Image layerinfo on plain image failed"

LAYER_INFO_NGINX="$($CTR images layerinfo ${NGINX})"
failExit $? "Image layerinfo on plain image failed"

setupPGP() {
	GPGHOMEDIR=${WORKDIR}/gpg2

	if [ -z "$(type -P gpg2)" ]; then
		failExit 1 "Missing gpg2 executable."
	fi

	mkdir -p ${GPGHOMEDIR}
	failExit $? "Could not create GPG2 home directory"
	gpg2 --home ${GPGHOMEDIR} --import <(echo "${GPGTESTKEY1}" | base64 -d) &>/dev/null
	failExit $? "Could not import GPG2 test key 1"
	gpg2 --home ${GPGHOMEDIR} --import <(echo "${GPGTESTKEY2}" | base64 -d) &>/dev/null
	failExit $? "Could not import GPG2 test key 1"
}

testPGP() {
	setupPGP
	echo "Testing PGP type of encryption on ${NGINX}"

	# nginx has large layers that are worth testing
	$CTR images encrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--platform linux/amd64 \
		--recipient testkey1@key.org \
		${NGINX} ${NGINX_ENC}
	failExit $? "Image encryption of ${NGINX} with PGP failed"

	LAYER_INFO_NGINX_ENC="$($CTR images layerinfo ${NGINX_ENC})"
	failExit $? "Image layerinfo on PGP encrypted image failed"

	diff <(echo "${LAYER_INFO_NGINX}"     | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_NGINX_ENC}" | gawk '{print $3}' )
	failExit $? "Image layerinfo on PGP encrypted image shows differences in architectures"

	diff <(echo "${LAYER_INFO_NGINX_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
	     <(echo -n "ENCRYPTIONpgp" )
	failExit $? "Image layerinfo on PGP encrypted image shows unexpected encryption"

	$CTR images decrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--key <(echo "${GPGTESTKEY1}" | base64 -d) \
		${NGINX_ENC} ${NGINX_DEC}
	failExit $? "Image decryption with PGP failed"

	LAYER_INFO_NGINX_DEC="$($CTR images layerinfo ${NGINX_DEC})"
	failExit $? "Image layerinfo on decrypted image failed (PGP)"

	diff <(echo "${LAYER_INFO_NGINX}") <(echo "${LAYER_INFO_NGINX_DEC}")
	failExit $? "Image layerinfos are different (PGP)"

	$CTR images rm --sync ${NGINX_DEC} ${NGINX_ENC} &>/dev/null
	sleep ${SLEEP_TIME}

	echo "PASS: PGP Type of encryption on ${NGINX}"
	echo
	echo "Testing PGP Type of encryption on ${ALPINE}"

	$CTR images encrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--recipient testkey1@key.org \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption with PGP failed"

	LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
	failExit $? "Image layerinfo on PGP encrypted image failed"

	diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
	failExit $? "Image layerinfo on PGP encrypted image shows differences in architectures"

	diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
	     <(echo -n "ENCRYPTIONpgp" )
	failExit $? "Image layerinfo on PGP encrypted image shows unexpected encryption"

	$CTR images decrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--key <(echo "${GPGTESTKEY1}" | base64 -d) \
		${ALPINE_ENC} ${ALPINE_DEC}
	failExit $? "Image decryption with PGP failed"

	LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
	failExit $? "Image layerinfo on decrypted image failed (PGP)"

	diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
	failExit $? "Image layerinfos are different (PGP)"

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE} &>/dev/null
	sleep ${SLEEP_TIME}

	echo "PASS: PGP Type of encryption"
	echo
	echo "Testing image export and import using ${ALPINE_ENC}"

	$CTR images export ${WORKDIR}/${ALPINE_ENC_EXPORT_NAME} ${ALPINE_ENC}
	failExit $? "Could not export ${ALPINE_ENC}"

	# remove ${ALPINE} and ${ALPINE_ENC} to clear cached and so we need to decrypt
	$CTR images rm --sync ${ALPINE} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}

	$CTR images import \
		--base-name ${ALPINE_ENC_IMPORT_BASE} \
		${WORKDIR}/${ALPINE_ENC_EXPORT_NAME} &>/dev/null
	if [ $? -eq 0 ]; then
		failExit 1 "Import of encrypted image without passing PGP key should not have succeeded"
	fi
	# need a delay here
	sleep ${SLEEP_TIME}

	MSG=$($CTR images import \
		--base-name ${ALPINE_ENC_IMPORT_BASE} \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--key <(echo "${GPGTESTKEY1}" | base64 -d) \
		${WORKDIR}/${ALPINE_ENC_EXPORT_NAME} 2>&1)
	failExit $? "Import of PGP encrypted image should have worked\n$MSG"
	# need a delay here
	sleep ${SLEEP_TIME}

	LAYER_INFO_ALPINE_ENC_NEW="$($CTR images layerinfo ${ALPINE_ENC})"
	failExit $? "Image layerinfo on imported image failed (PGP)"

	diff <(echo "${LAYER_INFO_ALPINE_ENC_NEW}" | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_ALPINE_ENC}"     | gawk '{print $3}' )
	failExit $? "Image layerinfo on PGP encrypted image shows differences in architectures"

	diff <(echo "${LAYER_INFO_ALPINE_ENC_NEW}") <(echo "${LAYER_INFO_ALPINE_ENC}")
	failExit $? "Image layerinfos are different (PGP)"

	# restore ${ALPINE}
	MSG=$($CTR images decrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--key <(echo "${GPGTESTKEY1}" | base64 -d) \
		${ALPINE_ENC} ${ALPINE} 2>&1)
	failExit $? "Image decryption with PGP failed\n$MSG"
	sleep ${SLEEP_TIME}

	LAYER_INFO_ALPINE_NEW="$($CTR images layerinfo ${ALPINE})"
	failExit $? "Image layerinfo on imported image failed (PGP)"

	diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_ALPINE_NEW}" | gawk '{print $3}' )
	failExit $? "Image layerinfo on plain ${ALPINE} image shows differences in architectures"

	echo "PASS: Export and import of PGP encrypted image"
	echo
	echo "Testing adding a PGP recipient"
	$CTR images encrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--key <(echo "${GPGTESTKEY1}" | base64 -d) \
		--recipient testkey2@key.org ${ALPINE_ENC}
	failExit $? "Adding recipient to PGP encrypted image failed"
	sleep ${SLEEP_TIME}

	LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo -n ${ALPINE_ENC})"
	failExit $? "Image layerinfo on PGP encrypted image failed"

	diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
	failExit $? "Image layerinfo on PGP encrypted image shows differences in architectures"

	diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $6 $7}' | sort | uniq | tr -d '\n') \
	     <(echo -n "0x6d6d5017a3752cbd,0xb0310f009d3abc2fRECIPIENTS" )
	failExit $? "Image layerinfo on PGP encrypted image shows unexpected recipients"

	LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo --gpg-homedir ${GPGHOMEDIR} --gpg-version 2 ${ALPINE_ENC})"
	failExit $? "Image layerinfo on PGP encrypted image failed"

	diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $6 $7}' | sort | uniq | tr -d '\n') \
	     <(echo -n "RECIPIENTStestkey1@key.org,testkey2@key.org" )
	failExit $? "Image layerinfo on PGP encrypted image shows unexpected recipients"

	for privkey in ${GPGTESTKEY1} ${GPGTESTKEY2}; do
		$CTR images decrypt \
			--gpg-homedir ${GPGHOMEDIR} \
			--gpg-version 2 \
			--key <(echo "${privkey}" | base64 -d) \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with PGP failed"
		sleep ${SLEEP_TIME}

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (PGP)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (PGP)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "PGP Decryption worked."
		sleep ${SLEEP_TIME}
	done

	echo "PASS: PGP Type of decryption after adding recipients"
	echo

	$CTR images rm --sync ${ALPINE_ENC} ${ALPINE_DEC} &>/dev/null
	sleep ${SLEEP_TIME}
}

createJWEKeys() {
	echo "Generating keys for JWE encryption"

	PRIVKEYPEM=${WORKDIR}/mykey.pem
	PRIVKEYDER=${WORKDIR}/mykey.der
	PRIVKEYPK8PEM=${WORKDIR}/mykeypk8.pem
	PRIVKEYPK8DER=${WORKDIR}/mykeypk8.der

	PUBKEYPEM=${WORKDIR}/mypubkey.pem
	PUBKEYDER=${WORKDIR}/mypubkey.der

	PRIVKEY2PEM=${WORKDIR}/mykey2.pem
	PUBKEY2PEM=${WORKDIR}/mypubkey2.pem

	PRIVKEY3PASSPEM=${WORKDIR}/mykey3pass.pem
	PRIVKEY3PASSWORD="1234"
	PUBKEY3PEM=${WORKDIR}/pubkey3.pem

	PRIVKEYJWK=${WORKDIR}/mykey.jwk
	PUBKEYJWK=${WORKDIR}/mypubkey.jwk

	MSG="$(openssl genrsa -out ${PRIVKEYPEM} 2>&1)"
	failExit $? "Could not generate private key\n$MSG"

	MSG="$(openssl rsa -inform pem -outform der -in ${PRIVKEYPEM} -out ${PRIVKEYDER} 2>&1)"
	failExit $? "Could not convert private key to DER format\n$MSG"

	MSG="$(openssl pkcs8 -topk8 -nocrypt -inform pem -outform pem -in ${PRIVKEYPEM} -out ${PRIVKEYPK8PEM} 2>&1)"
	failExit $? "Could not convert private key to PKCS8 PEM format\n$MSG"

	MSG="$(openssl pkcs8 -topk8 -nocrypt -inform pem -outform der -in ${PRIVKEYPEM} -out ${PRIVKEYPK8DER} 2>&1)"
	failExit $? "Could not convert private key to PKCS8 DER format\n$MSG"

	MSG="$(openssl rsa -inform pem -outform pem -pubout -in ${PRIVKEYPEM} -out ${PUBKEYPEM} 2>&1)"
	failExit $? "Could not write public key in PEM format\n$MSG"

	MSG="$(openssl rsa -inform pem -outform der -pubout -in ${PRIVKEYPEM} -out ${PUBKEYDER} 2>&1)"
	failExit $? "Could not write public key in PEM format\n$MSG"

	MSG="$(openssl genrsa -out ${PRIVKEY2PEM} 2>&1)"
	failExit $? "Could not generate 2nd private key\n$MSG"

	MSG="$(openssl rsa -inform pem -outform pem -pubout -in ${PRIVKEY2PEM} -out ${PUBKEY2PEM} 2>&1)"
	failExit $? "Could not write 2nd public key in PEM format\n$MSG"

	MSG="$(openssl genrsa -aes256 -passout pass:${PRIVKEY3PASSWORD} -out ${PRIVKEY3PASSPEM} 2>&1)"
	failExit $? "Could not generate 3rd private key\n$MSG"

	MSG="$(openssl rsa -inform pem -outform pem -passin pass:${PRIVKEY3PASSWORD} -pubout -in ${PRIVKEY3PASSPEM} -out ${PUBKEY3PEM} 2>&1)"
	failExit $? "Could not write 3rd public key in PEM format\n$MSG"

	echo "${JWKTESTKEY1}" > ${PRIVKEYJWK}
	echo "${JWKTESTPUBKEY1}" > ${PUBKEYJWK}
}

testJWE() {
	createJWEKeys
	echo "Testing JWE type of encryption"

	for recipient in ${PUBKEYDER} ${PUBKEYPEM}; do

		$CTR images encrypt \
			--recipient ${recipient} \
			${ALPINE} ${ALPINE_ENC}
		failExit $? "Image encryption with JWE failed; public key: ${recipient}"

		LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
		failExit $? "Image layerinfo on JWE encrypted image failed; public key: ${recipient}"

		diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
		     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
		failExit $? "Image layerinfo on JWE encrypted image shows differences in architectures"

		diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
		     <(echo -n "ENCRYPTIONjwe" )
		failExit $? "Image layerinfo on JWE encrypted image shows unexpected encryption"

		for privkey in ${PRIVKEYPEM} ${PRIVKEYDER} ${PRIVKEYPK8PEM} ${PRIVKEYPK8DER}; do
			$CTR images decrypt \
				--key ${privkey} \
				${ALPINE_ENC} ${ALPINE_DEC}
			failExit $? "Image decryption with JWE failed: private key: ${privkey}"

			LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
			failExit $? "Image layerinfo on decrypted image failed (JWE)"

			diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
			failExit $? "Image layerinfos are different (JWE)"

			$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
			echo "Decryption with ${privkey} worked."
			sleep ${SLEEP_TIME}
		done
		$CTR images rm --sync ${ALPINE_ENC} &>/dev/null
		echo "Encryption with ${recipient} worked"
		sleep ${SLEEP_TIME}
	done

	$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
	sleep ${SLEEP_TIME}

	echo "PASS: JWE Type of encryption"
	echo

	echo "Testing adding a JWE recipient"
	$CTR images encrypt \
		--recipient ${recipient} \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption with JWE failed; public key: ${recipient}"

	$CTR images encrypt \
		--key ${PRIVKEYPEM} \
		--recipient ${PUBKEY2PEM} \
		--recipient ${PUBKEY3PEM} \
		${ALPINE_ENC}
	failExit $? "Adding recipient to JWE encrypted image failed"
	sleep ${SLEEP_TIME}

	for privkey in ${PRIVKEYPEM} ${PRIVKEY2PEM} ${PRIVKEY3PASSPEM}; do
		local key=${privkey}
		if [ "${privkey}" == "${PRIVKEY3PASSPEM}" ]; then
			key=${privkey}:pass=${PRIVKEY3PASSWORD}
		fi
		$CTR images decrypt \
			--key ${key} \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with JWE failed: private key: ${privkey}"

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (JWE)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (JWE)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "Decryption with ${privkey} worked."
		sleep ${SLEEP_TIME}
	done

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}

	echo "PASS: JWE Type of decryption after adding recipients"
	echo
	echo "Testing JWE encryption with a JWK"

	# The JWK needs a separate test since it's a different key than the other ones
	for recipient in ${PUBKEYJWK}; do
		$CTR images encrypt \
			--recipient ${recipient} \
			${ALPINE} ${ALPINE_ENC}
		failExit $? "Image encryption with JWE failed; public key: ${recipient}"

		LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
		failExit $? "Image layerinfo on JWE encrypted image failed; public key: ${recipient}"

		diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
		     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
		failExit $? "Image layerinfo on JWE encrypted image shows differences in architectures"

		diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
		     <(echo -n "ENCRYPTIONjwe" )
		failExit $? "Image layerinfo on JWE encrypted image shows unexpected encryption"

		for privkey in ${PRIVKEYJWK}; do
			$CTR images decrypt \
				--key ${privkey} \
				${ALPINE_ENC} ${ALPINE_DEC}
			failExit $? "Image decryption with JWE failed: private key: ${privkey}"

			LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
			failExit $? "Image layerinfo on decrypted image failed (JWE)"

			diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
			failExit $? "Image layerinfos are different (JWE)"

			$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
			echo "Decryption with ${privkey} worked."
			sleep ${SLEEP_TIME}
		done
		$CTR images rm --sync ${ALPINE_ENC} &>/dev/null
		echo "Encryption with ${recipient} worked"
		sleep ${SLEEP_TIME}
	done

	echo "PASS: JWE encryption with a JWK"

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}
}

setupPKCS7() {
	echo "Generating certs for PKCS7 encryption"

	CACERT=${WORKDIR}/cacert.pem
	CAKEY=${WORKDIR}/cacertkey.pem
	CLIENTCERT=${WORKDIR}/clientcert.pem
	CLIENTCERTKEY=${WORKDIR}/clientcertkey.pem
	CLIENTCERTCSR=${WORKDIR}/clientcert.csr

	CLIENT2CERT=${WORKDIR}/client2cert.pem
	CLIENT2CERTKEY=${WORKDIR}/client2certkey.pem
	CLIENT2CERTCSR=${WORKDIR}/client2cert.csr

	local CFG="
[req]
distinguished_name = dn
[dn]
[ext]
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:TRUE
"
	MSG="$(openssl req -config <(echo "${CFG}") -newkey rsa:2048 \
		-x509 -extensions ext -days 365 -nodes -keyout ${CAKEY} -out ${CACERT} \
		-subj '/CN=foo/' 2>&1)"
	failExit $? "Could not create root CA's certificate\n${MSG}"

	MSG="$(openssl genrsa -out ${CLIENTCERTKEY} 2048 2>&1)"
	failExit $? "Could not create client key\n$MSG"
	MSG="$(openssl req -new -key ${CLIENTCERTKEY} -out ${CLIENTCERTCSR} -subj '/CN=bar/' 2>&1)"
	failExit $? "Could not create client ertificate signing request\n$MSG"
	MSG="$(openssl x509 -req -in ${CLIENTCERTCSR} -CA ${CACERT} -CAkey ${CAKEY} -CAcreateserial \
		-out ${CLIENTCERT} -days 10 -sha256 2>&1)"
	failExit $? "Could not create client certificate\n$MSG"

	MSG="$(openssl genrsa -out ${CLIENT2CERTKEY} 2048 2>&1)"
	failExit $? "Could not create client2 key\n$MSG"
	MSG="$(openssl req -new -key ${CLIENT2CERTKEY} -out ${CLIENT2CERTCSR} -subj '/CN=bar/' 2>&1)"
	failExit $? "Could not create client2 certificate signing request\n$MSG"
	MSG="$(openssl x509 -req -in ${CLIENT2CERTCSR} -CA ${CACERT} -CAkey ${CAKEY} -CAcreateserial \
		-out ${CLIENT2CERT} -days 10 -sha256 2>&1)"
	failExit $? "Could not create client2 certificate\n$MSG"
}

testPKCS7() {
	setupPKCS7

	echo "Testing PKCS7 type of encryption"

	for recipient in ${CLIENTCERT}; do
		$CTR images encrypt \
			--recipient ${recipient} \
			${ALPINE} ${ALPINE_ENC}
		failExit $? "Image encryption with PKCS7 failed; public key: ${recipient}"

		LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
		failExit $? "Image layerinfo on PKCS7 encrypted image failed; public key: ${recipient}"

		diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
		     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
		failExit $? "Image layerinfo on PKCS7 encrypted image shows differences in architectures"

		diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
		     <(echo -n "ENCRYPTIONpkcs7" )
		failExit $? "Image layerinfo on PKCS7 encrypted image shows unexpected encryption"

		for privKeyAndRecipient in "${CLIENTCERTKEY}:${CLIENTCERT}"; do
			privkey="$(echo ${privKeyAndRecipient} | cut -d ":" -f1)"
			recp="$(echo ${privKeyAndRecipient} | cut -d ":" -f2)"
			$CTR images decrypt \
				--dec-recipient ${recipient} \
				--key ${privkey} \
				${ALPINE_ENC} ${ALPINE_DEC}
			failExit $? "Image decryption with PKCS7 failed: private key: ${privkey}"

			LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
			failExit $? "Image layerinfo on decrypted image failed (PKCS7)"

			diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
			failExit $? "Image layerinfos are different (PKCS7)"

			$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
			echo "Decryption with ${privkey} worked."
			sleep ${SLEEP_TIME}
		done
		$CTR images rm --sync ${ALPINE_ENC} &>/dev/null
		echo "Encryption with ${recipient} worked"
		sleep ${SLEEP_TIME}
	done

	echo "PASS: PKCS7 Type of encryption"
	echo

	echo "Testing adding a PKCS7 recipient"
	$CTR images encrypt \
		--recipient ${CLIENTCERT} \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption with PKCS7 failed; public key: ${recipient}"

	$CTR images encrypt \
		--key ${CLIENTCERTKEY} \
		--dec-recipient ${CLIENTCERT} \
		--recipient ${CLIENT2CERT} \
		${ALPINE_ENC}
	failExit $? "Adding recipient to PKCS7 encrypted image failed"
	sleep ${SLEEP_TIME}

	for privKeyAndRecipient in "${CLIENTCERTKEY}:${CLIENTCERT}" "${CLIENT2CERTKEY}:${CLIENT2CERT}"; do
		privkey="$(echo ${privKeyAndRecipient} | cut -d ":" -f1)"
		recp="$(echo ${privKeyAndRecipient} | cut -d ":" -f2)"
		$CTR images decrypt \
			--key ${privkey} \
			--dec-recipient ${recp} \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with PKCS7 failed: private key: ${privkey}"

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (PKCS7)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (PKCS7)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "Decryption with ${privkey} worked."
		sleep ${SLEEP_TIME}
	done

	echo "PASS: PKCS7 Type of decryption after adding recipients"
	echo

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}
}

testPGPandJWEandPKCS7() {
	local ctr

	createJWEKeys
	setupPGP
	setupPKCS7

	echo "Testing large recipient list"
	$CTR images encrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--recipient testkey1@key.org \
		--recipient testkey2@key.org \
		--recipient ${PUBKEYPEM} \
		--recipient ${PUBKEY2PEM} \
		--recipient ${CLIENTCERT} \
		--recipient ${CLIENT2CERT} \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption to many different recipients failed"
	LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
	failExit $? "Image layerinfo on multi-recipient encrypted image failed; public key: ${recipient}"

	diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
	     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
	failExit $? "Image layerinfo on multi-recipient encrypted image shows differences in architectures"

	diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
	     <(echo -n "ENCRYPTIONjwe,pgp,pkcs7" )

	$CTR images rm --sync ${ALPINE_ENC} &>/dev/null
	echo "Encryption to multiple different types of recipients worked."
	sleep ${SLEEP_TIME}


	echo "Testing adding first PGP and then JWE and PKCS7 recipients"
	$CTR images encrypt \
		--gpg-homedir ${GPGHOMEDIR} \
		--gpg-version 2 \
		--recipient testkey1@key.org \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption with PGP failed; recipient: testkey1@key.org"
	sleep ${SLEEP_TIME}

	ctr=0
	for recipient in ${PUBKEYPEM} testkey2@key.org ${PUBKEY2PEM} ${CLIENTCERT} ${CLIENT2CERT}; do
		$CTR images encrypt \
			--gpg-homedir ${GPGHOMEDIR} \
			--gpg-version 2 \
			--recipient ${recipient} \
			--key <(echo "${GPGTESTKEY1}" | base64 -d) \
			${ALPINE_ENC}
		failExit $? "Adding ${recipient} failed"
		sleep ${SLEEP_TIME}

		LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
		failExit $? "Image layerinfo on multi-recipient encrypted image failed; public key: ${recipient}"

		diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
		     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
		failExit $? "Image layerinfo on multi-recipient encrypted image shows differences in architectures"

		if [ $ctr -lt 3 ]; then
			diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
			     <(echo -n "ENCRYPTIONjwe,pgp" )
		else
			diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
			     <(echo -n "ENCRYPTIONjwe,pgp,pkcs7" )
		fi
		failExit $? "Image layerinfo on JWE encrypted image shows unexpected encryption (ctr=$ctr)"
		ctr=$((ctr + 1))
	done

	# everyone must be able to decrypt it -- first JWE ...
	for privkey in ${PRIVKEYPEM} ${PRIVKEY2PEM}; do
		$CTR images decrypt \
			--key ${privkey} \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with JWE failed: private key: ${privkey}"
		sleep ${SLEEP_TIME}

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (JWE)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (JWE)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "JWE Decryption with ${privkey} worked."
		sleep ${SLEEP_TIME}
	done

	# ... then pgp
	for privkey in ${GPGTESTKEY1} ${GPGTESTKEY2}; do
		$CTR images decrypt \
			--gpg-homedir ${GPGHOMEDIR} \
			--gpg-version 2 \
			--key <(echo "${privkey}" | base64 -d) \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with PGP failed"
		sleep ${SLEEP_TIME}

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (PGP)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (PGP)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "PGP Decryption worked."
		sleep ${SLEEP_TIME}
	done

	# and then pkcs7
	for privKeyAndRecipient in "${CLIENTCERTKEY}:${CLIENTCERT}" "${CLIENT2CERTKEY}:${CLIENT2CERT}"; do
		privkey="$(echo ${privKeyAndRecipient} | cut -d ":" -f1)"
		recp="$(echo ${privKeyAndRecipient} | cut -d ":" -f2)"
		$CTR images decrypt \
			--key ${privkey} \
			--dec-recipient ${recp} \
			${ALPINE_ENC} ${ALPINE_DEC}
		failExit $? "Image decryption with PKCS7 failed: private key: ${privkey}"

		LAYER_INFO_ALPINE_DEC="$($CTR images layerinfo ${ALPINE_DEC})"
		failExit $? "Image layerinfo on decrypted image failed (PKCS7)"

		diff <(echo "${LAYER_INFO_ALPINE}") <(echo "${LAYER_INFO_ALPINE_DEC}")
		failExit $? "Image layerinfos are different (PKCS7)"

		$CTR images rm --sync ${ALPINE_DEC} &>/dev/null
		echo "PKCS7 decryption with ${privkey} worked."
		sleep ${SLEEP_TIME}
	done

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}


	echo "Testing adding first JWE and then PGP and PKCS7 recipients"
	$CTR images encrypt \
		--recipient ${PUBKEYPEM} \
		${ALPINE} ${ALPINE_ENC}
	failExit $? "Image encryption with JWE failed; public key: ${recipient}"
	sleep ${SLEEP_TIME}

	ctr=0
	for recipient in testkey1@key.org testkey2@key.org ${PUBKEY2PEM} ${CLIENTCERT} ${CLIENT2CERT}; do
		$CTR images encrypt \
			--gpg-homedir ${GPGHOMEDIR} \
			--gpg-version 2 \
			--recipient ${recipient} \
			--key ${PRIVKEYPEM} \
			${ALPINE_ENC}
		failExit $? "Adding ${recipient} failed"
		sleep ${SLEEP_TIME}

		LAYER_INFO_ALPINE_ENC="$($CTR images layerinfo ${ALPINE_ENC})"
		failExit $? "Image layerinfo on JWE encrypted image failed; public key: ${recipient}"

		diff <(echo "${LAYER_INFO_ALPINE}"     | gawk '{print $3}') \
		     <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $3}' )
		failExit $? "Image layerinfo on JWE encrypted image shows differences in architectures"

		if [ $ctr -lt 3 ]; then
			diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
			     <(echo -n "ENCRYPTIONjwe,pgp" )
		else
			diff <(echo "${LAYER_INFO_ALPINE_ENC}" | gawk '{print $5}' | sort | uniq | tr -d '\n') \
			     <(echo -n "ENCRYPTIONjwe,pgp,pkcs7" )
		fi
		failExit $? "Image layerinfo on JWE encrypted image shows unexpected encryption"
		ctr=$((ctr + 1))
	done

	echo "PASS: Test with JWE, PGP, and PKCS7 recipients"

	$CTR images rm --sync ${ALPINE_DEC} ${ALPINE_ENC} &>/dev/null
	sleep ${SLEEP_TIME}
}

testPGP
testJWE
testPKCS7
testPGPandJWEandPKCS7
