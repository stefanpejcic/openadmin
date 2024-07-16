$(document).ready(function() {

// SERVICES STATUS
function fetchServices() {
    $.ajax({
        url: '/json/services-status',
        type: 'GET',
        dataType: 'json',
        success: function (data) {
            var services = {
                'panel': {
                    'name': 'OpenPanel',
                    'icon': '<svg version="1.0" style="vertical-align:middle;" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 213.000000 215.000000" preserveAspectRatio="xMidYMid meet"><g transform="translate(0.000000,215.000000) scale(0.100000,-0.100000)" fill="currentColor" stroke="none"><path d="M990 2071 c-39 -13 -141 -66 -248 -129 -53 -32 -176 -103 -272 -158 -206 -117 -276 -177 -306 -264 -17 -50 -19 -88 -19 -460 0 -476 0 -474 94 -568 55 -56 124 -98 604 -369 169 -95 256 -104 384 -37 104 54 532 303 608 353 76 50 126 113 147 184 8 30 12 160 12 447 0 395 -1 406 -22 461 -34 85 -98 138 -317 264 -104 59 -237 136 -295 170 -153 90 -194 107 -275 111 -38 2 -81 0 -95 -5z m205 -561 c66 -38 166 -95 223 -127 l102 -58 0 -262 c0 -262 0 -263 -22 -276 -13 -8 -52 -31 -88 -51 -36 -21 -126 -72 -200 -115 l-135 -78 -3 261 -3 261 -166 95 c-91 52 -190 109 -219 125 -30 17 -52 34 -51 39 3 9 424 256 437 255 3 0 59 -31 125 -69z"></path></g></svg>'
                },
                'docker': {
                    'name': 'Docker Engine',
                    'icon': '<svg width="24" height="24" viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg" fill="none"><path fill="#2396ED" d="M12.342 4.536l.15-.227.262.159.116.083c.28.216.869.768.996 1.684.223-.04.448-.06.673-.06.534 0 .893.124 1.097.227l.105.057.068.045.191.156-.066.2a2.044 2.044 0 01-.47.73c-.29.299-.8.652-1.609.698l-.178.005h-.148c-.37.977-.867 2.078-1.702 3.066a7.081 7.081 0 01-1.74 1.488 7.941 7.941 0 01-2.549.968c-.644.125-1.298.187-1.953.185-1.45 0-2.73-.288-3.517-.792-.703-.449-1.243-1.182-1.606-2.177a8.25 8.25 0 01-.461-2.83.516.516 0 01.432-.516l.068-.005h10.54l.092-.007.149-.016c.256-.034.646-.11.92-.27-.328-.543-.421-1.178-.268-1.854a3.3 3.3 0 01.3-.81l.108-.187zM2.89 5.784l.04.007a.127.127 0 01.077.082l.006.04v1.315l-.006.041a.127.127 0 01-.078.082l-.039.006H1.478a.124.124 0 01-.117-.088l-.007-.04V5.912l.007-.04a.127.127 0 01.078-.083l.039-.006H2.89zm1.947 0l.039.007a.127.127 0 01.078.082l.006.04v1.315l-.007.041a.127.127 0 01-.078.082l-.039.006H3.424a.125.125 0 01-.117-.088L3.3 7.23V5.913a.13.13 0 01.085-.123l.039-.007h1.413zm1.976 0l.039.007a.127.127 0 01.077.082l.007.04v1.315l-.007.041a.127.127 0 01-.078.082l-.039.006H5.4a.124.124 0 01-.117-.088l-.006-.04V5.912l.006-.04a.127.127 0 01.078-.083l.039-.006h1.413zm1.952 0l.039.007a.127.127 0 01.078.082l.007.04v1.315a.13.13 0 01-.085.123l-.04.006H7.353a.124.124 0 01-.117-.088l-.006-.04V5.912l.006-.04a.127.127 0 01.078-.083l.04-.006h1.412zm1.97 0l.039.007a.127.127 0 01.078.082l.006.04v1.315a.13.13 0 01-.085.123l-.039.006H9.322a.124.124 0 01-.117-.088l-.006-.04V5.912l.006-.04a.127.127 0 01.078-.083l.04-.006h1.411zM4.835 3.892l.04.007a.127.127 0 01.077.081l.007.041v1.315a.13.13 0 01-.085.123l-.039.007H3.424a.125.125 0 01-.117-.09l-.007-.04V4.021a.13.13 0 01.085-.122l.039-.007h1.412zm1.976 0l.04.007a.127.127 0 01.077.081l.007.041v1.315a.13.13 0 01-.085.123l-.039.007H5.4a.125.125 0 01-.117-.09l-.006-.04V4.021l.006-.04a.127.127 0 01.078-.082l.039-.007h1.412zm1.953 0c.054 0 .1.037.117.088l.007.041v1.315a.13.13 0 01-.085.123l-.04.007H7.353a.125.125 0 01-.117-.09l-.006-.04V4.021l.006-.04a.127.127 0 01.078-.082l.04-.007h1.412zm0-1.892c.054 0 .1.037.117.088l.007.04v1.316a.13.13 0 01-.085.123l-.04.006H7.353a.124.124 0 01-.117-.088l-.006-.04V2.128l.006-.04a.127.127 0 01.078-.082L7.353 2h1.412z"/></svg>'
                },
                'nginx': {
                    'name': 'Nginx Web Server',
                    'icon': '<svg width="24" height="24" viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg"><title>file_type_nginx</title><path d="M15.948,2h.065a10.418,10.418,0,0,1,.972.528Q22.414,5.65,27.843,8.774a.792.792,0,0,1,.414.788c-.008,4.389,0,8.777-.005,13.164a.813.813,0,0,1-.356.507q-5.773,3.324-11.547,6.644a.587.587,0,0,1-.657.037Q9.912,26.6,4.143,23.274a.7.7,0,0,1-.4-.666q0-6.582,0-13.163a.693.693,0,0,1,.387-.67Q9.552,5.657,14.974,2.535c.322-.184.638-.379.974-.535" style="fill:#019639"/><path d="M8.767,10.538q0,5.429,0,10.859a1.509,1.509,0,0,0,.427,1.087,1.647,1.647,0,0,0,2.06.206,1.564,1.564,0,0,0,.685-1.293c0-2.62-.005-5.24,0-7.86q3.583,4.29,7.181,8.568a2.833,2.833,0,0,0,2.6.782,1.561,1.561,0,0,0,1.251-1.371q.008-5.541,0-11.081a1.582,1.582,0,0,0-3.152,0c0,2.662-.016,5.321,0,7.982-2.346-2.766-4.663-5.556-7-8.332A2.817,2.817,0,0,0,10.17,9.033,1.579,1.579,0,0,0,8.767,10.538Z" style="fill:#fff"/></svg>'
                },
                'mysql': {
                    'name': 'MySQL Database',
                    'icon': '<svg width="24" height="24" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path fill="#00758F" fill-rule="evenodd" d="M5.46192862,4.04007684 C5.18892668,4.03501656 4.99575061,4.06967946 4.79169495,4.11446291 L4.79169495,4.15152944 L4.82901449,4.15152944 C4.95944313,4.41909158 5.18943271,4.591394 5.35034952,4.82188962 C5.47407329,5.08262038 5.59817658,5.34297163 5.72227987,5.60332288 C5.73493056,5.5909252 5.74745474,5.57865403 5.75959941,5.56625635 C5.99047454,5.40394797 6.0957283,5.14410275 6.09471625,4.74737704 C6.00211318,4.64996671 5.98832392,4.52826705 5.90837155,4.4122602 C5.80235875,4.25754224 5.59615247,4.17012595 5.46192862,4.04007684 L5.46192862,4.04007684 Z M23.478665,23.1369293 C23.6543831,23.2658398 23.772161,23.4657208 24,23.5466852 L24,23.5093657 C23.8800714,23.3573044 23.8495833,23.1474294 23.7395222,22.9880306 C23.5786054,22.8271138 23.4164236,22.6655645 23.2555068,22.5040152 C22.7821179,21.8759083 22.1818425,21.3245911 21.5432356,20.8663831 C21.0345512,20.5006515 19.8944709,20.0072745 19.6819392,19.4148426 C19.6697946,19.4021919 19.6571439,19.3896677 19.6444932,19.3770171 C20.0054174,19.3365348 20.4283301,19.2059797 20.7614228,19.1165393 C21.3210894,18.9665021 21.8214243,19.0054662 22.3990549,18.8560615 C22.6600387,18.781296 22.9203899,18.7066569 23.1808677,18.6329033 L23.1808677,18.4834987 C22.8887632,18.1836773 22.6805328,17.7869515 22.3622414,17.5155942 C21.5283078,16.8061434 20.6188495,16.0966926 19.6818127,15.5056522 C19.1626283,15.1774933 18.5200996,14.9645821 17.969415,14.6865199 C17.7842089,14.5931578 17.4590861,14.5444526 17.3365009,14.3887226 C17.0476856,14.0198284 16.8899314,13.5523853 16.6667732,13.1228943 C16.1997097,12.2230506 15.740363,11.2403448 15.3263059,10.293567 C15.044322,9.6481287 14.8597484,9.01154587 14.5076796,8.43227067 C12.8174206,5.65329311 10.9976185,3.97581132 8.17942382,2.3270466 C7.57927498,1.97649592 6.85742648,1.83809735 6.09471625,1.65719245 C5.68546635,1.6325236 5.27545742,1.60734872 4.86620752,1.58267987 C4.61635635,1.47831166 4.35651113,1.17267094 4.12184079,1.02427832 C3.18796669,0.434503045 0.792811133,-0.848656668 0.10157731,0.838313141 C-0.335124586,1.90286889 0.753847001,2.94174374 1.14361483,3.48142227 C1.4172493,3.85980447 1.76704094,4.2842352 1.96287366,4.70967798 C2.09127818,4.98938478 2.11316388,5.27010364 2.22385744,5.56600333 C2.49432924,6.29518923 2.7293791,7.08838764 3.07929725,7.76241652 C3.25653344,8.10322617 3.45173363,8.46263233 3.67539786,8.76738751 C3.81265788,8.95449125 4.04720171,9.03684725 4.08401522,9.32578906 C3.85465817,9.64749617 3.84150145,10.1466925 3.7125909,10.5541713 C3.13065906,12.3887747 3.35014857,14.6686824 4.19660638,16.0266077 C4.45594557,16.443195 5.06773305,17.3374725 5.90837155,16.9942592 C6.64375629,16.6946908 6.47980332,15.76613 6.69018433,14.9469976 C6.73749792,14.760906 6.70865434,14.624405 6.80176344,14.5003017 L6.80176344,14.5373682 C7.02542767,14.9840642 7.2488389,15.4307601 7.47199711,15.8773296 C7.96815726,16.6759678 8.84826592,17.5111665 9.59415073,18.0739958 C9.98037636,18.3659737 10.2848785,18.8709894 10.7852134,19.0419002 L10.7852134,19.0040746 L10.7478939,19.0040746 C10.6504835,18.8536579 10.4989282,18.790531 10.3759635,18.6694638 C10.0844916,18.3836847 9.76050733,18.0287063 9.51938514,17.7014329 C8.84080201,16.780589 8.24153872,15.7725818 7.69553484,14.7235864 C7.43455106,14.2224925 7.20785066,13.6697838 6.98785512,13.1600874 C6.90322199,12.9633691 6.90423404,12.6662043 6.72737736,12.5643663 C6.48650818,12.9378147 6.13190928,13.2401663 5.94556458,13.6811694 C5.64776729,14.386319 5.60943569,15.2461865 5.49899515,16.1379338 C5.43371758,16.1614641 5.46268766,16.1453977 5.42422956,16.1750003 C4.90555118,16.0502645 4.72350772,15.5164053 4.53096418,15.0584502 C4.04378602,13.9006589 3.95333357,12.0360734 4.38206553,10.7030699 C4.4930121,10.3583386 4.99499157,9.27202362 4.79131543,8.95347919 C4.69441112,8.63544079 4.37510765,8.45187925 4.19635337,8.20885945 C3.97420721,7.90853201 3.75332613,7.5134509 3.59974672,7.16644241 C3.20150293,6.26368901 3.01528474,5.25024206 2.59540827,4.33749461 C2.39451528,3.90142525 2.0550972,3.45966308 1.77627595,3.07166635 C1.46759906,2.64204884 1.12185564,2.32578153 0.882884062,1.80583808 C0.797744903,1.62126448 0.681991069,1.32587082 0.808244978,1.13598393 C0.848094658,1.00783242 0.905022773,0.954446496 1.03190922,0.912572704 C1.24810955,0.746089595 1.84889092,0.967982736 2.07394674,1.06147135 C2.67055338,1.30929841 3.16924367,1.54548684 3.67489184,1.88035066 C3.91740561,2.04126747 4.16295554,2.35272751 4.45607208,2.43887872 L4.79118892,2.43887872 C5.31568662,2.5591868 5.90280525,2.47645128 6.39200751,2.62509691 C7.25744137,2.8881048 8.0329288,3.29722819 8.73719284,3.74202653 C10.8826237,5.09653615 12.6370217,7.02526068 13.8370664,9.32578906 C14.030116,9.69620133 14.1138635,10.0496617 14.2836358,10.4427187 C14.6265961,11.2350315 15.0591233,12.0501156 15.4004389,12.825097 C15.7408691,13.5978013 16.0728232,14.3779695 16.5541821,15.0213837 C16.8071959,15.3594102 17.7850944,15.5408211 18.2297663,15.7288104 C18.5412263,15.8602511 19.0514287,15.9976376 19.3460633,16.1750003 C19.9100312,16.5151775 20.4556556,16.9197466 20.9842015,17.292183 C21.2483479,17.4785277 22.0606489,17.886639 22.1006251,18.2223884 C20.7916579,18.1877255 19.7916207,18.3092986 18.9366869,18.6695903 C18.6936671,18.7716814 18.3064295,18.7747176 18.2664533,19.0787137 C18.4000446,19.2186304 18.4211712,19.4281259 18.527437,19.6000488 C18.7309867,19.9304848 19.0755915,20.3728795 19.3833829,20.6053993 C19.7195118,20.8590456 20.0657612,21.130403 20.4255469,21.3498925 C21.0663045,21.7407989 21.7818276,21.9638306 22.3984224,22.3551165 C22.7632683,22.5861182 23.1241926,22.8764515 23.478665,23.1369293 L23.478665,23.1369293 Z"/></svg>'
                },
                'ufw': {
                    'name': 'Uncomplicated Firewall',
                    'icon': '<svg height="24" width="24" version="1.1" id="Layer_1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"viewBox="0 0 512.117 512.117" xml:space="preserve"><g><g><path style="fill:#C6564E;" d="M293.054,341.122c0.415-0.98,0.847-1.96,1.289-2.931c1.774-3.919,3.681-7.786,5.87-11.511c1.209,4.634,2.631,9.154,4.202,13.542c0.026,0.062,0.044,0.132,0.071,0.194c1.58,4.405,3.302,8.669,5.102,12.738c11.37,25.715,25.777,43.944,25.918,44.12c-0.018-0.106-0.035-0.194,0.018,0.026c-2.41-15.554-3.231-30.234-2.71-44.138v-0.009c0.23-6.312,3.681-31.391,4.891-36.555c0.106-0.433,0.194-0.883,0.3-1.315c2.798-11.591,6.647-22.493,11.396-32.742h-84.577v70.621h23.746C289.832,349.076,291.385,345.068,293.054,341.122"/><path style="fill:#C6564E;" d="M335.523,397.299l-0.018-0.018C335.532,397.405,335.567,397.564,335.523,397.299"/></g><path style="fill:#F0CE49;" d="M467.862,273.713c-4.078,36.988-26.483,70.621-26.483,70.621c0-84.78-35.31-132.414-35.31-132.414c-50.132,39.998-83.783,100.043-70.541,185.379c0.071,0.486-0.115-0.486,0,0c0,0-25.185-31.7-35.31-70.621c-26.209,44.694-23.296,104.51,11.988,144.702c47.263,53.848,130.842,54.237,178.626,1.156C531.791,427.048,507.842,314.541,467.862,273.713"/><path style="fill:#ED7161;" d="M88.276,70.679H0V17.713C0,7.959,7.901,0.058,17.655,0.058h70.621V70.679z"/><polygon style="fill:#D75A4A;" points="88.276,70.679 264.828,70.679 264.828,0.058 88.276,0.058 	"/><path style="fill:#C6564E;" d="M406.069,70.679H264.828V0.058h123.586c9.754,0,17.655,7.901,17.655,17.655V70.679z"/><polygon style="fill:#D75A4A;" points="317.793,141.299 406.069,141.299 406.069,70.679 317.793,70.679 	"/><polygon style="fill:#ED7161;" points="141.241,141.299 317.793,141.299 317.793,70.679 141.241,70.679 	"/><polygon style="fill:#BA4D45;" points="0,141.299 141.241,141.299 141.241,70.679 0,70.679 	"/><polygon style="fill:#D75A4A;" points="0,211.92 88.276,211.92 88.276,141.299 0,141.299 	"/><polygon style="fill:#C6564E;" points="88.276,211.92 264.828,211.92 264.828,141.299 88.276,141.299 	"/><polygon style="fill:#BA4D45;" points="264.828,211.92 406.069,211.92 406.069,141.299 264.828,141.299 	"/><polygon style="fill:#D75A4A;" points="141.241,282.541 317.793,282.541 317.793,211.92 141.241,211.92 	"/><polygon style="fill:#ED7161;" points="0,282.541 141.241,282.541 141.241,211.92 0,211.92 	"/><polygon style="fill:#C6564E;" points="0,353.161 88.276,353.161 88.276,282.541 0,282.541 	"/><polygon style="fill:#BA4D45;" points="88.276,353.161 264.828,353.161 264.828,282.541 88.276,282.541 	"/><path style="fill:#ED7161;" d="M0,353.161h141.241v70.621H17.655C7.901,423.782,0,415.881,0,406.127V353.161z"/><path style="fill:#D75A4A;" d="M288.573,353.161H141.24v70.621h145.858C280.972,400.698,281.511,376.007,288.573,353.161"/><path style="fill:#ED7161;" d="M406.069,211.92h-88.276v70.621h31.603C362.522,254.195,382.42,230.793,406.069,211.92"/></g></svg>'
                },
                'bind9': {
                    'name': 'BIND DNS Server',
                    'icon': '<svg width="24" height="24" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><defs><style>.cls-1{fill:#4285f4;}.cls-2{fill:#669df6;}.cls-3{fill:#aecbfa;}.cls-4{fill:#ffffff;}</style></defs><title>Icon_24px_DNS_Color</title><g data-name="Product Icons"><g data-name="colored-32/dns"><g ><polygon id="Fill-1" class="cls-1" points="13 18 11 18 11 8 13 8 13 18"/><polygon id="Fill-2" class="cls-2" points="2 21 22 21 22 19 2 19 2 21"/><polygon id="Fill-3" class="cls-3" points="10 22 14 22 14 18 10 18 10 22"/></g></g><rect class="cls-3" x="2" y="2" width="20" height="6"/><rect class="cls-2" x="12" y="2" width="10" height="6"/><rect class="cls-4" x="4" y="4" width="2" height="2"/><rect class="cls-3" x="2" y="10" width="20" height="6"/><rect class="cls-2" x="12" y="10" width="10" height="6"/><rect class="cls-4" x="4" y="12" width="2" height="2"/></g></svg>'
                }
            };

            var table = '<table class="table table-striped"><thead><tr><th>Service</th><th>Status</th><th>Actions</th></tr></thead><tbody>';

            Object.keys(services).forEach(function (service) {
                var displayName = services[service].icon + ' ' + services[service].name;
                var status = data[service];
                var actionsHtml = '';

                if (status === 'Inactive') {
                    actionsHtml = '<a href="/service/start/' + service + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Start ' + services[service].name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-player-play" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M7 4v16l13 -8z" /></svg></a>';
                } else {
                    actionsHtml = '<a href="/service/restart/' + service + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Restart ' + services[service].name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-rotate-clockwise-2" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M9 4.55a8 8 0 0 1 6 14.9m0 -4.45v5h5" /><path d="M5.63 7.16l0 .01" /><path d="M4.06 11l0 .01" /><path d="M4.63 15.1l0 .01" /><path d="M7.16 18.37l0 .01" /><path d="M11 19.94l0 .01" /></svg></a>' +
                        '<a href="/service/stop/' + service + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Stop ' + services[service].name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-player-stop" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M5 5m0 2a2 2 0 0 1 2 -2h10a2 2 0 0 1 2 2v10a2 2 0 0 1 -2 2h-10a2 2 0 0 1 -2 -2z" /></svg></a>';
                }

                // Define CSS class based on status
                var statusClass = '';
                if (status === 'Active') {
                    statusClass = 'success';
                } else if (status === 'Inactive') {
                    statusClass = 'danger';
                } else {
                    statusClass = 'warning';
                }

                var rowHtml = '<tr><td>' + displayName + '</td><td><span class="badge bg-' + statusClass + ' me-1"></span>' + status + '</td><td>' + actionsHtml + '</td></tr>';

                table += rowHtml;
            });

            table += '</tbody></table>';

            // Replace the content of a div with the table
            $('#service-table').html(table);
        },
        error: function () {
            // Handle any errors here
            console.log('Error fetching service status data.');
        }
    });
}

fetchServices();
setInterval(fetchServices, 5000); 

        });



    function updateUserActivityTable() {
        $.ajax({
            url: '/combined_activity_logs',
            type: 'GET',
            dataType: 'json',
            success: function (data) {
                // Clear existing table rows
                $('#activity-table tbody').empty();



            if (data.combined_logs.length > 0) {
                // If there is data, hide the placeholder
                $('#shouldbehidden').hide();
                
                // Iterate over the logs and update the table
                data.combined_logs.forEach(function (log) {
                    // Parse the log entry
                    var parts = log.split(' ');

                    // Extract date
                    var date = parts.slice(0, 3).join(' ');

                    // Extract IP
                    var ip = parts[3];

                    // Extract userAndActivity
                    var userAndActivity = parts.slice(4).join(' ');

                    // Extract username based on role
                    var usernameMatch;
                    if (parts[4] === 'Administrator') {
                        // If the role is Administrator, username is the 4th word
                        usernameMatch = parts[5].match(/(\w+)/);
                    } else {
                        // If the role is User, username is the word after User
                        usernameMatch = userAndActivity.match(/User (\w+)/);
                    }

                    var username = usernameMatch ? usernameMatch[1] : '';

                    var formattedDate = moment(date, 'YYYY-MM-DD HH:mm:ss');
                    var now = moment();

                    var diffMinutes = Math.abs(now.diff(formattedDate, 'minutes'));
                    var isOnline = diffMinutes <= 90;

                    // Construct the avatar content based on online status and user role
                    var avatarContent;
                    if (parts[4] === 'Administrator') {
                        avatarContent = '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-crown" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M12 6l4 6l5 -4l-2 10h-14l-2 -10l5 4z"></path></svg>';
                    } else {
                        avatarContent = username[0].toUpperCase();
                    }

                    // Construct the avatar class based on online status
                    var avatarClass = isOnline ? '<span class="badge bg-primary"></span>' : '';
                    var displayUsernameClass = (parts[4] === 'Administrator') ? 'text-teal bg-dark' : '';
                    var displayUsername = (parts[4] === 'Administrator') ?
                        '<span class="avatar ' + displayUsernameClass + '">' + avatarContent + '</span>' :
                        '<a style="text-decoration:none;" href="/users/' + username + '#activity_log"><span class="avatar ' + displayUsernameClass + '">' + avatarContent + '</span></a>';

                    // Build the row in the specified format
                    var row = '<div class="row">' +
                        '<div class="col-auto">' + displayUsername + '</div>' +
                        '<div class="col"><div class="text-truncate">' + userAndActivity.replace(/user (\w+)/i, 'User <strong>$1</strong>') + '</div><div class="text-secondary">' + formattedDate.format('D.M.Y H:mm:ss') + '</div></div>' +
                        '<div class="col-auto align-self-center">' + avatarClass + '</div></div>';

                    $('#activity-table .divide-y').append(row);
                });
            } else {
                // If there is no data, show the placeholder
                $('#shouldbehidden').show();
            }









            },
            error: function (error) {
                console.error('Error fetching user activity:', error);
            }
        });
    }

    // Initial call to load user activity
    updateUserActivityTable();


        // Use Ajax to get Docker context data
        $(document).ready(function() {
            $.ajax({
                url: '/json/docker-context',
                type: 'GET',
                dataType: 'json',
                success: function(data) {
                    updateDockerContextsTable(data);
                },
                error: function(error) {
                    console.log(error);
                }
            });
        });

        // Update the Bootstrap table with the received data
        function updateDockerContextsTable(data) {
            var tableBody = $('#docker-contexts-table tbody');
            tableBody.empty(); // Clear the table body

            $.each(data, function(index, context) {
                var row = '<tr>' +
                    '<td>' + context.Name + '</td>' +
                    '<!--td>' + context.Description + '</td-->' +
                    '<td>' + context.DockerEndpoint + '</td>' +
                    '<td>' + (context.Current ? 'Yes' : 'No') + '</td>' +
                    '<!--td>' + context.Error + '</td-->' +
                    '</tr>';
                tableBody.append(row);
            });
        }



    // Use Ajax to get disk usage data
    $(document).ready(function() {
        $.ajax({
            url: '/json/disk-usage',
            type: 'GET',
            dataType: 'json',
            success: function(data) {
                updateDiskUsageTable(data);
            },
            error: function(error) {
                console.log(error);
            }
        });
    });

// Update the Bootstrap table with the received data
function updateDiskUsageTable(data) {
    var tableBody = $('#disk-usage-table tbody');
    tableBody.empty(); // Clear the table body

    $.each(data, function(index, disk_info) {
        // Limit 'device' and 'mountpoint' to 50 characters
        var truncatedDevice = disk_info.device.length > 18 ? disk_info.device.substring(0, 18) + '...' : disk_info.device;
        var truncatedMountpoint = disk_info.mountpoint.length > 40 ? disk_info.mountpoint.substring(0, 40) + '...' : disk_info.mountpoint;

        // Create a table row ('<tr>') with information from the 'disk_info' object
        var row = '<tr>' +
            '<td ' + (disk_info.device.length > 18 ? 'data-bs-toggle="tooltip" data-placement="top" title="' + disk_info.device + '"' : '') + '>' + truncatedDevice + '</td>' +
            '<td ' + (disk_info.mountpoint.length > 40 ? 'data-bs-toggle="tooltip" data-placement="top" title="' + disk_info.mountpoint + '"' : '') + '><div class="progressbg"><div class="progress progressbg-progress"><div class="progress-bar bg-primary-lt" style="width: ' + disk_info.percent + '%" role="progressbar" aria-valuenow="' + disk_info.percent + '" aria-valuemin="0" aria-valuemax="100" aria-label="' + disk_info.percent + '% Used"><span class="visually-hidden">' + disk_info.percent + '% Complete</span></div></div><div class="progressbg-text">' + truncatedMountpoint + '</div></div></td>' +
            '<!--td>' + disk_info.fstype + '</td-->' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.used) + '</td>' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.total) + '</td>' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.free) + '</td>' +
            '<td class="text-secondary ' + getColorClass(disk_info.percent) + '">' + disk_info.percent + '</td>' +
            '</tr>';


        tableBody.append(row);
    });

    // Enable Bootstrap tooltips
    $('[data-bs-toggle="tooltip"]').tooltip();
}
    // Determine the color class based on usage percentage
    function getColorClass(percentage) {
        if (percentage > 90) {
            return 'text-danger'; // Red color
        } else if (percentage > 80) {
            return 'text-warning'; // Orange color
        } else {
            return ''; // Default color
        }
    }

    // Format disk size to GB or TB
    function formatDiskSize(bytes) {
        if (bytes > 1024 * 1024 * 1024 * 1024) {
            var tb = bytes / (1024 * 1024 * 1024 * 1024); // Convert to TB
            return tb.toFixed(2) + ' TB'; // Display in TB with two decimal places
        } else {
            var gb = bytes / (1024 * 1024 * 1024); // Convert to GB
            return gb.toFixed(2) + ' GB'; // Display in GB with two decimal places
        }
    }



    // Function to update RAM info
    function updateRamInfo() {
        $.get("/json/ram-usage", function(data) {
            var html = data.human_readable_info.used + " / " + data.human_readable_info.total + " (" + data.human_readable_info.percent + ")";
            var percentString = data.human_readable_info.percent;
            var percent = parseInt(percentString.slice(0, -1));
            $("#human-readable-info").html(html);

            var ramIndicator = $("#ramIndicator");
            var ramIconColor = $("#ramIconColor");
            ramIndicator.removeClass("bg-primary");
            ramIconColor.removeClass("bg-primary-lt");

            if (percent < 60) {
                ramIndicator.addClass("bg-success");
                ramIconColor.addClass("bg-success-lt");
            } else if (percent >= 60 && percent <= 80) {
                ramIndicator.addClass("bg-warning");
                ramIconColor.addClass("bg-warning-lt");
            } else {
                ramIndicator.addClass("bg-danger");
                ramIconColor.addClass("bg-danger-lt");
            }
        });
    }


        function getServerLoad() {
            fetch('/get_server_load')
                .then(response => response.json())
                .then(loadData => {
                    const load1min = parseFloat(loadData.load1min);
                    const load5min = parseFloat(loadData.load5min);
                    const load15min = parseFloat(loadData.load15min);
                    document.getElementById('load1min').textContent = load1min.toFixed(2);
                    document.getElementById('load5min').textContent = load5min.toFixed(2);
                    document.getElementById('load15min').textContent = load15min.toFixed(2);



                    const loadDifference = load1min - load5min;

                    var serverloadIndicator = $("#serverloadIndicator");
                    var loadIconColor = $("#loadIconColor");
                    serverloadIndicator.removeClass("bg-primary-lt");
                    loadIconColor.removeClass("bg-primary-lt");


                    const loadIndicator = document.getElementById('load-indicator');
                    if (loadDifference >= 0.1) {
                        serverloadIndicator.addClass("bg-warning");
                        loadIconColor.addClass("bg-warning-lt");
                        loadIndicator.innerHTML = '<span class="arrow-up">&#8593;</span>';
                    } else if (loadDifference <= -0.1) {
                        serverloadIndicator.addClass("bg-success");
                        loadIconColor.addClass("bg-success-lt");
                        loadIndicator.innerHTML = '<span class="arrow-down">&#8595;</span>';
                    } else {
                    serverloadIndicator.addClass("bg-primary");
                    loadIconColor.addClass("bg-primary-lt");
                        loadIndicator.innerHTML = '';
                    }
                })
                .catch(error => console.error(error));
        }

        function refreshServerLoadandRamUsage() {
            getServerLoad();
            updateRamInfo();
            setTimeout(refreshServerLoadandRamUsage, 2000);
        }

        window.onload = function() {
            refreshServerLoadandRamUsage();
        };

