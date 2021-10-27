

#include <openssl/x509v3.h>
#include <string.h>

const unsigned char * get_extention(X509 *x, int NID, int *data_len){
    int loc;
    ASN1_OCTET_STRING *octet_str;
    long xlen;
    int tag, xclass;

    loc = X509_get_ext_by_NID( x, NID, -1);
    X509_EXTENSION *ex = X509_get_ext(x, loc);
    octet_str = X509_EXTENSION_get_data(ex);
	*data_len = octet_str->length;
    return octet_str->data;
}

// Copied from https://github.com/libtor/openssl/blob/master/demos/x509/mkcert.c#L153
int add_custom_ext(X509 *cert, int nid,unsigned char *value, int len)
{
	X509_EXTENSION *ex;
	ASN1_OCTET_STRING *os = ASN1_OCTET_STRING_new();
	ASN1_OCTET_STRING_set(os,value,len);   
	X509V3_CTX ctx;
	/* This sets the 'context' of the extensions. */
	/* No configuration database */
	X509V3_set_ctx_nodb(&ctx);
	/* Issuer and subject certs: both the target since it is self signed,
	 * no request and no CRL
	 */
	X509V3_set_ctx(&ctx, cert, cert, NULL, NULL, 0);
	// ref http://openssl.6102.n7.nabble.com/Adding-a-custom-extension-to-a-CSR-td47446.html
	ex = X509_EXTENSION_create_by_NID( NULL, nid, 0, os); 
	if (!X509_add_ext(cert,ex,-1))
		return 0;

	X509_EXTENSION_free(ex);
	return 1;
}